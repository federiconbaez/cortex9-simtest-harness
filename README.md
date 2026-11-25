# cortex9-simtest-harness

Harness de *deterministic simulation testing* (DST) en Go. Tomás un
sistema distribuido chico, lo enchufás por puertos y el harness lo corre
dentro de un mundo simulado: reloj virtual, red que pierde, duplica,
demora y reordena mensajes, y nodos que se caen y reinician. Todo sale
de **una semilla** y de **un solo hilo**, así que un fallo se reproduce
con una línea de comando. Al final, un checker de linealizabilidad
decide si la historia de operaciones que vieron los clientes es válida.

Proyecto de estudio, Apache-2.0, Go 1.24+, solo biblioteca estándar.
Ref: CX9-6201. Contrato: [CONTRACT.md](CONTRACT.md). Decisiones:
[ADR 0001](docs/adr/0001-event-loop-single-thread.md),
[ADR 0002](docs/adr/0002-checker-wgl-por-clave.md).

## Qué hace

- **Núcleo puro (`internal/domain`)**: `Clock` virtual, RNG SplitMix64
  propio, scheduler con cola de prioridad por `(at, seq)`, red simulada
  `FaultyNetwork`, crash/restart, `FaultPlan`, traza canónica, historia y
  checker. Sin `os`, `io`, `net`, `time`, `math/rand`, `sync` ni `go`.
- **Puertos**: `Process` (nodo), `Client`, `Env` (lo que el simulador le
  da al nodo: `Send`, `After`, `Storage`, `Rand`, `Now`), `Transport`,
  `Storage` y `SUT`.
- **Plan de fallos** reproducible: derivado de la semilla o explícito,
  por ejemplo
  `-faults 'delay=1-14,drop=26,dup=69,reorder=3,crash=77,crash-every=95,restart=15-62'`.
- **SUT de referencia (`internal/adapters/kv`)**: KV primary-backup con
  primario fijo `n0`, persistencia atómica de valor + secuencia del
  cliente (dedupe de reintentos) y replicación con reintentos.
- **Bug plantado (`kv-lazy`)**: el primario confirma el `put` desde
  memoria y lo baja a disco cada 60 ticks. Si se cae en esa ventana,
  pierde una escritura ya confirmada.
- **Checker**: Wing-Gong/Lowe con memoización `(bitset, estado)`,
  particionado por clave. Los `put` sin respuesta son opcionales y los
  `get` sin respuesta se ignoran.
- **CLI `simtest`**: una corrida, barrido de semillas o chequeo de un
  archivo de historia. Cada fallo imprime la línea de repro.

## Lo que NO hace

- No prueba goroutines reales ni data races del código de producción:
  el SUT tiene que estar escrito como máquina de estados contra los puertos.
- No minimiza (*shrinking*) los fallos. Eso existe en otro repositorio
  mío, `cortex9-dst`; este no lo modifica ni reutiliza su código.
- El KV no tiene elección de líder ni failover; los backups no sirven
  lecturas. Es un SUT de juguete para ejercitar el harness.
- No hay particiones de red ni discos con escrituras rotas.
- El modelo del checker es un KV de registros; otro SUT necesita otro modelo.

## Uso

```bash
go run ./cmd/simtest -sut kv-lazy -seed 12                 # una corrida
go run ./cmd/simtest -sut kv-lazy -seed 1 -seeds 1000      # barrido
go run ./cmd/simtest -sut kv-lazy -seed 12 -trace t.trace -history h.history
go run ./cmd/simtest -check-history h.history              # solo checker
```

Salida real de la primera línea:

```
sut=kv-lazy seed=12 plan=delay=2-2,drop=21,dup=92,reorder=13,reorder-extra=7,crash=59,crash-every=79,restart=15-75,max-crashed=1
events=515 sent=178 dropped=5 dup=17 reordered=3 crashes=7 ops=36 explored=32 digest=02554e85f2f4dc79
FAIL seed=12 key=k1 repro: go run ./cmd/simtest -sut kv-lazy -replicas 3 -seed 12 -clients 3 -ops 12 -keys 2 -horizon 3000 -think 40
```

Códigos de salida: 0 linealizable, 1 violación, 2 error de uso.

## Números medidos

Medidos en la box de desarrollo (8 vCPU, Go 1.27.3) con la
configuración por defecto (3 servidores, 3 clientes, 12 ops por cliente,
2 claves, horizonte 3000 ticks, plan derivado de la semilla).

| Medición | Resultado |
|---|---|
| `kv-lazy`, semillas 1–1000 | 74 fallan (7,4 %); la primera es la **12** |
| `kv-lazy`, semillas 1001–2000 | 65 fallan (6,5 %) |
| `kv` correcto, semillas 1–1000 | 0 fallos |
| `kv` correcto, plan duro explícito, semillas 1–200 | 0 fallos |
| Barrido de 1000 semillas | ~1,3 s de reloj |
| Checker vs. fuerza bruta | coinciden en 3000 historias aleatorias por corrida de test (~1/3 linealizables) |
| Cobertura de `internal/domain` | 98,7 % de sentencias |
| Fuzz local, 25 s por target | `ParseTrace` 7,56 M, `ParseHistory` 9,30 M, `ParseFaultPlan` 8,87 M ejecuciones, 0 fallos |
| Tests | 35 funciones `Test` + 3 `Fuzz` (8 semillas de corpus) |

Con ~7 % de semillas que fallan, en promedio hacen falta unas 14
semillas para dar con el bug; en este rango apareció en la 12. El bug
solo aparece si el plan incluye crashes y el primario cae entre un `put`
confirmado y el siguiente volcado.

## Garantías verificadas por tests

- Misma semilla ⇒ misma traza byte a byte (propiedad + digest dorado).
- `(at, seq)` no decreciente en la traza; ningún evento con
  `at <= horizon` queda sin despachar; FIFO a igual tiempo.
- Sin reorden ni duplicados, cada enlace entrega en orden de envío.
- Los timers de una encarnación anterior no se disparan tras un restart;
  el `Storage` sobrevive al crash.
- La historia de un fallo se puede guardar, parsear y vuelve a fallar.
- Regla de dependencias: depguard + `internal/archtest`, que parsea el
  código y rechaza imports fuera de la lista blanca, `go`, `select` y
  canales en el dominio.

## Gates

`gofmt`, `go vet`, `staticcheck`, `golangci-lint` (depguard, gosec,
errorlint, revive), `go test -race`, cobertura del dominio, fuzz corto,
barrido del bug plantado y `gitleaks`, todo en GitHub Actions.

## Estructura

```
cmd/simtest              CLI (cableado de adapters)
internal/domain          núcleo puro: clock, rng, scheduler, red, fallos, traza, historia, checker
internal/app             harness: workload, registro de historia, Run/Explore, repro
internal/adapters/kv     SUT de referencia y variante kv-lazy
internal/adapters/memstore  puerto Storage en memoria
internal/archtest        regla de dependencias como test
```

## Cómo contarlo en una entrevista

1. **Problema**: los bugs de sistemas distribuidos dependen del orden de
   mensajes y del momento de un crash. Un test normal casi nunca los
   reproduce dos veces.
2. **Idea**: sacar todo el no determinismo del código y meterlo en una
   semilla. Un solo event loop, reloj virtual, RNG propio, y el SUT
   escrito como máquina de estados contra puertos. Con eso, una semilla
   es un repro.
3. **Diseño hexagonal**: el dominio no sabe de discos ni de red; el KV
   es un adapter que implementa `SUT`/`Process`/`Client`, y el storage
   es otro adapter. La regla de dependencias se verifica con un test que
   parsea el código, además del linter.
4. **Oráculo**: en vez de asserts a mano, verifico linealizabilidad con
   WGL + memoización, por clave (la linealizabilidad es local). Lo valido
   contra un verificador de fuerza bruta en miles de historias chicas.
5. **Evidencia**: planté un bug real (ack antes de persistir). El
   harness lo encuentra en ~7 % de las semillas y nunca marca la versión
   correcta en 1200 corridas. Puedo mostrar la línea de repro.
6. **Límites honestos**: no hay shrinking ni failover, y no reemplaza a
   `-race`. El siguiente paso sería minimizar el plan de fallos de una
   semilla que falla.

## Licencia

Apache-2.0. Ver [LICENSE](LICENSE).
