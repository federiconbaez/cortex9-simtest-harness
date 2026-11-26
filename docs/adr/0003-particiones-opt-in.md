# ADR 0003 — Particiones de red opt-in que no cambian las semillas existentes

- Estado: aceptado
- Ref: CX9-6207

## Contexto

La red simulada perdía, duplicaba, demoraba y reordenaba mensajes, y los nodos se caían, pero no
había particiones: un nodo vivo que no puede hablar con nadie durante un rato. Es el fallo más
común en sistemas distribuidos reales. Al agregarlo había una restricción: cada semilla publicada
(por ejemplo, la 12 de `kv-lazy`) y el digest dorado tienen que reproducir exactamente lo mismo
que antes.

## Decisión

- `FaultPlan` suma `partition` (por mil, por tick), `partition-every` y `partition-len=lo-hi`.
  En cada tick, si no hay una partición activa y sale el sorteo, el simulador aísla **un** nodo
  elegido al azar (los clientes también pueden quedar aislados) durante un tiempo dentro del
  rango. Después hay un `heal`. Nunca hay dos particiones a la vez.
- Un mensaje que cruza el corte se pierde si el corte está activo **cuando llegaría**. Así se
  modela un enlace caído: también se pierden los mensajes que ya estaban en vuelo. Queda en la
  traza como `drop ... partitioned`, y `isolate`/`heal` son tipos de evento nuevos.
- Es opt-in: `RandomFaultPlan` no activa particiones. Con `partition=0` no se agenda ningún tick
  ni se consume azar, así que las trazas sin particiones quedan idénticas byte a byte. Lo
  verifican el digest dorado y el barrido sin cambios (74/1000 en `kv-lazy`, primera semilla
  12).
- Forma canónica: con `partition=0` los otros tres campos valen 0 y no se imprimen. Con
  `partition>0` y sin período ni duración, se usan 100 y 20-80.

## Consecuencias

- Se puede probar que un SUT sigue siendo linealizable mientras un nodo está aislado. El `kv`
  correcto da 0 fallos en 1000 semillas con particiones y crashes.
- Con el SUT de referencia, las particiones no aumentan la detección del bug plantado: con el
  mismo plan, 143/1000 semillas fallan con particiones y 146/1000 sin ellas. Solo con
  particiones, sin crashes, fallan 0, porque el bug necesita que el primario pierda memoria.
  Queda documentado y no se presenta como más poder de detección.
- Solo se modela "un nodo contra el resto". No hay particiones arbitrarias (mayoría/minoría con
  varios nodos de cada lado), ni asimétricas (A llega a B pero B no a A), ni particiones
  parciales.
