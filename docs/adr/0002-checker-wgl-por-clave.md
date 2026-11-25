# ADR 0002 — Checker WGL con memoización, particionado por clave

- Estado: aceptado
- Ref: CX9-6201

## Contexto

Verificar linealizabilidad es NP-completo en general. Las historias del
harness tienen decenas de operaciones con reintentos y operaciones que
quedan sin respuesta al terminar la simulación.

## Decisión

- Algoritmo de Wing-Gong con la mejora de Lowe: lista doblemente
  enlazada de eventos call/return, backtracking con pila y caché de
  configuraciones `(bitset de ops linealizadas, estado del modelo)`.
- Linealizabilidad es *local* (Herlihy-Wing): una historia de un KV es
  linealizable si y solo si lo es la subhistoria de cada clave. Se
  verifica cada clave por separado con un modelo de registro.
- Operaciones pendientes: `put` con retorno `+∞` (puede o no haber
  ocurrido); `get` pendiente se descarta porque no observa nada.
- Un verificador de fuerza bruta (permutaciones, solo en tests) sobre el
  KV completo valida al checker en historias chicas.

## Consecuencias

- (+) Costo manejable para las historias del harness.
- (+) Al particionar se reporta la clave que viola linealizabilidad.
- (−) El modelo es específico de registro/KV; otro SUT necesita otro modelo.
