# CONTRACT — cortex9-simtest-harness

Ref: CX9-6201 · Licencia: Apache-2.0 · Go 1.24+, solo biblioteca estándar.

## 1. Propósito

Harness de *deterministic simulation testing* (DST) para sistemas
distribuidos pequeños escritos en Go. El sistema bajo prueba (SUT) se
enchufa por puertos; el harness le da un reloj virtual, una red simulada
con fallos y un almacenamiento durable simulado, todo movido por **una
sola semilla** y un **único hilo de ejecución**. Después verifica la
historia de operaciones con un checker de linealizabilidad.

## 2. Alcance

- Reloj virtual (`Clock`, `Time`, `Duration`) que solo avanza el scheduler.
- RNG propio sembrado (SplitMix64), estable entre versiones de Go.
- Scheduler/event loop single-thread: cola de prioridad por `(at, seq)`.
- Red simulada (`FaultyNetwork`, implementación del puerto `Transport`)
  con drop, duplicado, delay y reorden; crash/restart de nodos.
- `FaultPlan`: plan de fallos explícito (`-faults "drop=50,..."`) o
  derivado de la semilla; siempre imprimible en forma canónica.
- Particiones opt-in (CX9-6207, ADR 0003): un nodo aislado del resto durante
  `partition-len`. Se activan solo con `partition>0` en un plan explícito.
- Puertos del SUT: `Process` (nodo), `Client`, `Env`, `Storage`, `SUT`.
- SUT de referencia: KV replicado primary-backup (`kv`) y una variante
  con bug plantado (`kv-lazy`).
- Checker de linealizabilidad estilo Wing-Gong/Lowe (WGL) con
  memoización `(bitset, estado)`, particionado por clave.
- Traza textual canónica + parser; historia textual + parser.
- CLI `simtest`: una semilla, exploración de rango, chequeo de historia
  desde archivo. Cada fallo imprime una línea de repro.

## 3. No-objetivos

- No prueba goroutines reales ni data races del código de producción.
- No hay red real, ni sockets, ni tiempo de pared en el núcleo.
- No hay shrinking/minimización de fallos (eso vive en `cortex9-dst`,
  repositorio separado que este no modifica ni copia).
- El KV de referencia no tiene elección de líder ni failover: es un
  SUT de juguete para ejercitar el harness, no una base de datos.
- No hay particiones mayoría/minoría ni asimétricas (solo un nodo contra el
  resto), ni discos con escrituras rotas (*torn writes*).

## 4. Invariantes

- **I1 Determinismo:** misma semilla + misma configuración ⇒ traza
  byte a byte idéntica (y mismo digest FNV-64a).
- **I2 Orden del scheduler:** los eventos despachados tienen `(at, seq)`
  estrictamente creciente; a igual tiempo, FIFO por orden de agenda.
- **I3 Equidad/no inanición:** todo evento agendado con `at <= horizon`
  se despacha antes de terminar la corrida.
- **I4 Reloj monótono:** el reloj nunca retrocede (`ErrTimeTravel`).
- **I5 FIFO por enlace sin reorden:** con `reorder=0` y `dup=0`, los
  mensajes de un enlace `(from,to)` llegan en orden de envío.
- **I6 Timers de encarnaciones viejas** no se disparan tras un restart.
- **I7 Storage durable:** lo aplicado con `Storage.Apply` sobrevive al
  crash; el estado en memoria del proceso no.
- **I8 Checker:** coincide con un verificador de fuerza bruta en
  historias chicas; las operaciones sin respuesta son opcionales
  (un `put` pendiente puede o no haber tenido efecto; un `get`
  pendiente se ignora).
- **I9 Pureza:** `internal/domain` no importa I/O (`os`, `io`, `net`),
  ni `time`, `math/rand`, `sync`, ni otras capas; no usa `go`.
  Lo hacen cumplir depguard y `internal/archtest`.
- **I10 Particiones:** con un nodo aislado, ningún mensaje entre ese nodo y
  otro se entrega mientras dure el corte (los que estaban en vuelo se
  pierden). Hay como mucho una partición a la vez. Con `partition=0` no se
  consume azar: las trazas sin particiones no cambian.

## 5. Puertos

| Puerto      | Dirección | Quién lo implementa |
|-------------|-----------|---------------------|
| `Process`   | driven por el sim | nodos del SUT (`kv.server`) |
| `Client`    | driven por el harness | cliente del SUT (`kv.client`) |
| `Env`       | driving (lo da el sim) | `domain.Sim` |
| `Transport` | driven | `domain.FaultyNetwork` (o un fake) |
| `Storage`   | driven | `adapters/memstore` |
| `SUT`       | driven | `adapters/kv` |

## 6. Errores

`ErrTimeTravel`, `ErrDuplicateNode`, `ErrUnknownNode`, `ErrInvalidPlan`,
`ErrParse`, `ErrInvalidHistory`, `ErrInvalidConfig`. Se envuelven con
`%w`; la CLI sale con código 1 si la historia no es linealizable y 2 ante
error de uso/configuración.

## 7. Criterios de aceptación

1. CONTRACT.md y ADRs antes del código.
2. Tests de propiedad: determinismo (I1), orden (I2/I3), FIFO (I5),
   checker vs fuerza bruta (I8).
3. El harness encuentra el bug plantado de `kv-lazy` en un rango de
   semillas medido y no reporta falsos positivos sobre `kv` en el mismo
   rango; los números van al README.
4. Fuzz de `ParseTrace`, `ParseHistory` y `ParseFaultPlan` (round-trip),
   incluidas las claves de partición.
5. Gates: gofmt, vet, staticcheck, golangci-lint (con depguard),
   `go test -race`, cobertura de dominio (número real), gitleaks. CI verde.
6. Propiedad I10 sobre trazas y barrido del `kv` correcto con particiones +
   crashes en CI (0 fallos).
