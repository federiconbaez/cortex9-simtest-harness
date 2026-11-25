# ADR 0001 — Event loop single-thread y RNG propio

- Estado: aceptado
- Ref: CX9-6201

## Contexto

Para que una semilla reproduzca un fallo, cada decisión (latencia, drop,
crash, orden de entrega) tiene que depender solo de la semilla. Las
goroutines, `select`, el tiempo de pared y el orden de iteración de los
mapas rompen eso.

## Decisión

- El núcleo es un único event loop: cola de prioridad por `(at, seq)`,
  donde `seq` es un contador monotónico de agenda. No hay goroutines.
- Los procesos del SUT son máquinas de estado cooperativas: reaccionan a
  `Start`, `Receive` y `Timer` y devuelven el control.
- RNG propio (SplitMix64) en vez de `math/rand`: la secuencia queda fija
  aunque cambie la implementación de la biblioteca estándar.
- Toda iteración sobre conjuntos se hace en orden ordenado.

## Consecuencias

- (+) Misma semilla ⇒ traza idéntica; se verifica con un test de propiedad.
- (+) Los fallos se reproducen con una línea de comando.
- (−) El SUT tiene que escribirse contra los puertos; no se puede
  enchufar código con goroutines propias.
- (−) No detecta data races reales: para eso sigue existiendo `-race`.
