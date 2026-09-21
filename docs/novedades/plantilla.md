---
date: <YYYY-MM-DD>   # UTC day; the head is the last main commit before the next 00:00Z
range: <base-hash-or-tag>..<head-hash>
commits_no_merge: <N>
commits_total: <N total>
files: <N>
lines_added: <N>
lines_removed: <N>
---

# Lo nuevo en *Gentle AI*

**Novedades en main · <día> de <mes> de <año>**

**Rango:** `<base-hash-or-tag>..<head-hash>` · **<N> cambios** (no-merge) · **<N> archivos** ·
**+<N> / -<N>** líneas

<Una o dos frases: qué es este documento y por qué se puede leer sin saber programar.>

## En 30 segundos

<Un párrafo breve que resume la entrega completa: qué es Gentle AI en una frase, y qué trae esta
entrega (lo que se va a notar y lo que no).>

- **<Título corto del cambio 1 — el más visible primero>.** <Una frase: qué cambió, en el lenguaje
  del lector.>
- **<Título corto del cambio 2>.** <Una frase.>
- **<Título corto del cambio 3>.** <Una frase.>
- **<Título corto del cambio 4 — puede ser interno>.** <Una frase.>

## ¿Te afecta?

Busca tu caso. Si no aparece, no tienes que hacer nada: todo se actualiza por dentro.

| Si... | Qué cambia | Sección |
|---|---|---|
| <Perfil de usuario que le toca la sección 1, ej. "Usas X"> | <Qué cambia para ese perfil, en una frase.> | [1](#1--título-de-la-sección) |
| <Perfil de usuario que le toca la sección 2> | <Qué cambia para ese perfil, en una frase.> | [2](#2--título-de-la-segunda-sección) |

**Cómo leer este documento.** Cada sección empieza con una etiqueta que te dice si te toca:
**Te afecta si <condición>** o **Solo cambia por dentro**. Y cierra con unas **notas técnicas**
para quien quiera los detalles exactos. Si no las necesitas, sáltalas: el texto principal se
entiende solo.

## 1 · <Título de la sección, en lenguaje de lector no técnico>

**Te afecta si <condición>** (o **Solo cambia por dentro** si no aplica). <Una frase que resume la
sección completa; aparece también junto al número en el índice.>

<Párrafo de contexto: qué pasaba antes, en lenguaje llano. Se permite **negrita**, *cursiva* y
`código` inline.>

<Segundo párrafo: qué cambia ahora, y por qué le sirve al lector.>

> **<Etiqueta corta, ej. "Si no lo quieres">:** <Un dato puntual y accionable, ej. el comando exacto
> para revertir el cambio: `comando --flag`.>

*<Opcional: una analogía cotidiana que aterrice el concepto técnico sin tecnicismos.>*

**Notas técnicas:**
- **<Etiqueta técnica, ej. "Cambio">:** <Detalle exacto para quien quiere el nivel técnico, con
  `identificadores` reales.>
- **PR:** #<número>

## 2 · <Título de la segunda sección>

**Solo cambia por dentro.** <Una frase que resume esta sección.>

<Párrafo de contexto.>

- **<Punto uno.>** <Detalle.>
- **<Punto dos.>** <Detalle.>

### <Subtítulo opcional dentro de la sección, texto plano>

<Párrafo adicional si la sección necesita un segundo bloque de tema.>

**Notas técnicas:**
- **<Etiqueta técnica>:** <Detalle técnico.>
- **Issues:** #<número>, #<número>

## Glosario

- **<Término técnico usado en el documento>** — <Definición de una línea, en lenguaje llano.>

## Cierre

*<Frase de cierre que resume el espíritu de la entrega, puede usar énfasis.>*

— Así se construye.

## Anexo: los <N> commits

Agrupados por sección, con el mensaje original del historial (en inglés). Rango:
`<base-hash-or-tag>..<head-hash>`, rama `main` de `Gentleman-Programming/gentle-ai`.

### 01 · <título corto, igual al de la sección 1>

| Hash | Mensaje |
|---|---|
| `<hash corto>` | <mensaje de commit real, tal como aparece en git log> |

### 02 · <título corto, igual al de la sección 2>

| Hash | Mensaje |
|---|---|
| `<hash corto>` | <mensaje de commit real> |
