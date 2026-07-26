# La aventura de arreglar RDD

> Cómo dos días sin dormir, una comunidad entera y tres resets mensuales terminaron en una release. Para el detalle técnico, ver [organic-rdd.md](organic-rdd.md).

## Qué es RDD, en una frase

Cuando cambiás algo importante, alguien lo revisa antes de que salga. Eso es todo.

Lo difícil no es la idea, es que **no moleste**. Un sistema que te obliga a hacer ceremonia para cambiar una coma se desinstala en tres días. Uno que no te dice nada cuando tocás autenticación no sirve para nada.

## Cómo funciona ahora

Cambiás algo. La herramienta mira **qué** cambiaste, no cuánto.

- **Editaste un README** → no te pregunta nada. Cero ceremonia.
- **Escribiste mil líneas de documentación** → tampoco. El tamaño no importa.
- **Tocaste dos líneas de login** → ahí sí, cuatro revisores.

Y si no querés nada de esto:

```
gentle-ai review mode disable
```

Listo. Se apagó. **No es "apagado pero igual te molesto"** — es apagado. Hacés lo que quieras, y si lo prendés de nuevo te avisa que va a revalidar lo que quedó sin revisar.

---

## La parte que nadie cuenta

### Empezó mal

La primera versión de esto no la escribí sola. La hice con Codex GPT 5.6 en modo ultra, y salió una cosa enorme.

Había una auditoría que mencionaba requisitos de nivel empresa. El modelo **infirió** que hacía falta soportar HTTP, ejecución remota, toda una infraestructura para equipos grandes. Y la construyó. Completa. Coherente. Bien hecha.

Y no era lo que había que hacer.

Nadie la había pedido. Salió de deducir una necesidad a partir de un documento que hablaba de otra cosa. Después hubo que sacar todo, y sacar algo grande y bien hecho es más difícil que sacar algo roto, porque **parece que funciona**.

Eso me consumió **tres resets mensuales de Codex**.

### Y hubo que aprender a pedir

Lo que cambió no fue el modelo, fue cómo lo dirigía.

Con gentle-ai y las prácticas que veníamos armando: **fases con contratos claros, un solo escritor por carril, verificar antes de afirmar, y la regla de que si un test existente falla no se toca — se para y se reporta.**

Esa última regla sola frenó **nueve premisas equivocadas**. Nueve veces un agente iba a arreglar algo, un test viejo se puso en rojo, y resultó que el test tenía razón y el diagnóstico no.

Después de dos días trabajando como locos, todavía estamos al **66% del límite semanal**. La diferencia no fue el modelo. Fue el método.

---

## Lo que la comunidad encontró

Esta es la parte que más me gusta.

Sacamos una pre-release y la gente la rompió. En el buen sentido.

**@Wladimirfn, @Denver2828, @MarsSall y @Freedom2828** reportaron el mismo error desde cuatro ángulos. Parecía un bug de Windows. No lo era: pasaba cuando el commit revisado ya estaba publicado. Denver2828 llegó al mismo diagnóstico por su cuenta, compilando la rama con prints, y **su parche era idéntico al nuestro, línea por línea**.

**@ElCaaarnal** tipeó un flag a mano y chocó con algo que nosotros habíamos anunciado como arreglado. Tenía razón: habíamos arreglado que la herramienta dejara de *imprimirlo* mal, no que el parser lo aceptara. **El changelog prometió de más y él perdió tiempo por eso.**

**@ardelperal** reportó que un comando salía con código de éxito cuando debía fallar. Lo investigamos: era una trampa de medición. En bash, `$?` te da el estado del *último comando del pipe*, no del binario. Su reporte no era un bug, pero documentó una trampa que a cualquier otro le hubiera costado una tarde.

**@Blue-XL** encontró que una autorización deliberadamente falsa se aceptaba y se guardaba en el registro de auditoría como si fuera legítima. Peor que no tener el campo: una autorización ausente es honestamente ausente, **una equivocada miente**.

**@AlbertGC13** encontró dos cosas en Windows con una prolijidad que da gusto: separó explícitamente lo que había probado de lo que solo había leído en el código, y **aclaró qué no estaba afirmando**. Encontró que un rechazo de permisos de Git se convertía en un consejo imposible de seguir.

**@edwinsaavedran** mostró que cuatro defectos de macOS se habían escapado porque CI no corre en Darwin, y armó el caso con links a cada uno.

**@MarcosArispe, @dnlrsls, @GinoL221, @orlo-dragomir, @lu149e, @salema97, @diegofercho21323, @blickcbot, @Deco** y varios más siguieron probando refresh tras refresh.

Ninguno de esos hallazgos salió de una auditoría interna. **Salieron de gente usando la herramienta.**

---

## Las auditorías: las que sirvieron y las que no

### Las que sirvieron

Las mecánicas. Las que se derivan del código y no de una lista que alguien tiene que acordarse de actualizar.

Una recorre el árbol de sintaxis buscando mensajes de error que nombren un comando, y verifica que ese comando y esos flags **existan de verdad**. Encontró errores que apuntaban a cosas inexistentes.

Otra rechaza funciones nuevas que nadie llama. Cuando sacamos la limpieza de Codex, nos dijo que **quince funciones** quedaban muertas — todo un parser que existía solo para eso. Las borramos siguiendo esa evidencia.

Esa guarda tenía ocho horas de vida cuando encontró su primer hallazgo real.

### Las que no

Las que verificaban que algo **se emitiera**, nunca que sirviera.

El caso perfecto: había un mensaje que te decía "para salir de acá corré este comando". Había tests. Verificaban que el mensaje saliera, con el texto exacto. Todo verde.

**Nadie había corrido nunca el comando que el mensaje nombraba.**

Cuando lo corrimos, no funcionaba. Estábamos mandando gente a un callejón sin salida, con cobertura de tests en verde, durante meses.

Ahí nació la regla que gobierna todo el resto:

> **Un mensaje puede nombrar un comando solo si corriéndolo se resuelve el bloqueo.**

Nombrar un callejón es peor que no nombrar nada.

---

## El benchmark

En algún momento dejamos de discutir si estaba mejor y lo medimos.

La herramienta cuenta cuántas veces te trabás y, sobre todo, **cómo te trabás**:

- **En banda** — te frena y te dice qué correr para seguir
- **Fuera de banda** — te frena y no te dice nada
- **Sin salida** — te frena y no hay nada que puedas hacer

No mide velocidad. La velocidad depende del proveedor y del día; la fricción es tuya.

La primera medición: **seis bloqueos, todos fuera de banda.**

La última: **cero sin salida, y el único fuera de banda que queda sale con código de éxito** — o sea, ni siquiera es un bloqueo, es un informe que el analizador cuenta de más.

Un tester lo dijo mejor que nuestra propia herramienta: *"comunica correctamente el estado, pero no propone comando de continuación"*.

---

## Los errores que cometí yo

Porque si esto va a ser honesto, va completo.

**Escribí pasos de la guía sin correrlos.** Tres veces. Un tester los siguió, no funcionaban, y reportó el fallo. De ahí salió una regla nueva: antes de nombrar una continuación, ejecutala.

**Convertí un hallazgo en un parche de documentación.** Tres testers distintos no pudieron completar un flujo. En vez de tomar eso como el dato que era, escribí la receta en la guía. El maintainer me lo marcó: al hacer eso **destruí la medición** y escondí el defecto. Lo revertí. El defecto real era que la herramienta tenía un comando que emitía justo lo que hacía falta, y ninguna ruta te llevaba a él.

**Stageé un archivo sin mirar el diff** mientras un agente escribía en él. Me llevé 154 líneas de trabajo ajeno a medio hacer y pusheé una rama que no compilaba. Tengo trinquetes, guardas y tests que exigen que los comandos funcionen. **Nada de eso te protege de un `git add` apurado.**

**Perseguí un defecto que era mi propio error de medición.** Escribí la salida de un comando dentro del repositorio que estaba midiendo, eso agregó un archivo, cambió el estado, y el sistema me rechazó con razón. Perdí una hora. Pero salió algo bueno: ese rechazo tampoco explicaba nada, así que lo arreglamos y ahora está documentado como trampa en la guía.

---

## Dónde quedó

Los cuatro defectos de macOS: cerrados y verificados en hardware real, no en un perfil sintético.

Windows se actualiza solo por primera vez.

Codex arrancaba roto después de sincronizar y ahora **ni siquiera tocamos su archivo de configuración** — verificado con el mismo número de inodo antes y después, o sea que no lo abrimos para escribir, no es que escribimos lo mismo.

El kill switch es un kill switch.

Y quedan cosas abiertas, escritas en el documento técnico, porque una lista honesta de lo que falta vale más que una release que dice que está todo.

---

## Lo que aprendimos

**Un test que verifica que algo se emitió no verifica que sirva.** Esa distinción explica casi todos los defectos de esta rama.

**El código muerto que igual se documenta es mentira.** Había una función que instalaba dependencias. No la llamaba nadie. Los docs decían que la herramienta instalaba dependencias. Un usuario de Linux leyó eso y esperó que funcionara.

**Sobre-ingeniería es más difícil de sacar que un bug.** Un bug se ve. Una arquitectura entera que nadie pidió, bien construida y coherente, se defiende sola.

**La comunidad encuentra lo que las auditorías no.** Los cuatro reportes más valiosos de estos días salieron de gente usando la herramienta en su máquina, con su repo, con su configuración rara. Ninguna auditoría interna los hubiera encontrado, porque una auditoría busca lo que ya sabés buscar.

**Y la regla que se llevó todo por delante:** si le decís a alguien qué hacer, asegurate de que eso funcione.
