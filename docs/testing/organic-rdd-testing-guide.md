# 🧪 How to test — RDD Orgánico (pre-release 2.2.0-rc.1)

> Guía de testing comunitario para el candidato construido desde el PR [#1801](https://github.com/Gentleman-Programming/gentle-ai/pull/1801). Cada **Esperado** de esta guía fue validado contra salida real antes de publicarse. La guía usa un HOME temporal justamente para no tocar tu config real — no te saltees la preparación.

## Cómo obtener este binario

Los binarios están en la página de la pre-release: **https://github.com/Gentleman-Programming/gentle-ai/releases/tag/v2.2.0-rc.1**

1. Bajen el asset de su plataforma desde la sección Assets de esa página.
2. Verifiquen el checksum contra `SHA256SUMS.txt`:
   ```
   sha256sum -c SHA256SUMS.txt --ignore-missing
   ```
3. Guarden su binario actual y reemplacen:
   ```
   cp $(which gentle-ai) ~/gentle-ai.backup
   chmod +x gentle-ai_2.2.0-rc.1_<os>_<arch>
   mv gentle-ai_2.2.0-rc.1_<os>_<arch> $(which gentle-ai)
   ```
4. Confirmen: `gentle-ai --version` debe decir `2.2.0-rc.1`.
5. Para volver atrás cuando terminen: `mv ~/gentle-ai.backup $(which gentle-ai)`.

## Preparación (una sola vez)

1. Creen un HOME de prueba para no tocar su config real:
   ```
   export TESTHOME=$(mktemp -d) && export HOME=$TESTHOME
   ```
2. Creen un repo de prueba (el `.gitignore` evita que la config instalada se meta en los diffs):
   ```
   mkdir -p $HOME/demo && cd $HOME/demo && git init -b main && git config user.email t@t && git config user.name T && echo ".claude/" > .gitignore && echo hola > README.md && git add -A && git commit -m "inicio"
   ```

## Pasos para probar

### Flujo 1: Routing sin SDD (el fix principal)

1. [ ] `gentle-ai install --scope workspace --agents claude-code --components permissions` → **Esperado**: instala y termina con "You're ready", sin pedir nada de SDD.
2. [ ] Abran `$HOME/demo/.claude/CLAUDE.md` → **Esperado**: sección de routing con **direct inline**, **delegated direct** y **optional SDD**.
3. [ ] Busquen `WorkRun` o `work-capabilities` → **Esperado**: **cero resultados**. Si aparece, es bug.
4. [ ] Busquen `review mode` → **Esperado**: aparece `gentle-ai review mode enable|disable|status`.
5. [ ] Corran el mismo install de nuevo → **Esperado**: misma salida y los archivos NO cambian.

### Flujo 2: Kill switch

1. [ ] `gentle-ai review mode status --cwd $HOME/demo --json` → **Esperado**: efectivo `on`, con la fuente que lo decide.
2. [ ] `gentle-ai review mode disable --cwd $HOME/demo` → **Esperado**: confirma apagado.
3. [ ] `status` de nuevo → **Esperado**: efectivo `off`, fuente `global`.
4. [ ] `gentle-ai review start --cwd $HOME/demo` → **Esperado**: rechazado nombrando que las revisiones están apagadas. NO cuelga, NO revisa.
5. [ ] `enable` y `status` → **Esperado**: `on` otra vez.
6. [ ] `disable --scope clone`, clonen (`git clone $HOME/demo $HOME/demo2`) y `status` en `demo2` → **Esperado**: `demo2` da **on** — el apagado de un clon NO se hereda.
7. [ ] **Antes de seguir**: `enable --scope clone` en `demo` → **Esperado**: `on`.

### Flujo 3: Cambio solo de documentación (cero ceremonia)

1. [ ] Editen `README.md` (texto normal) y stageen **solo ese archivo**: `git add README.md`.
2. [ ] `gentle-ai review start --cwd $HOME/demo` → **Esperado**: `risk_level: low`, `selected_lenses: []` — cero reviewers, sin pregunta.

### Flujo 4: La revisión se elige por evidencia, no por tamaño

1. [ ] `mkdir -p internal/auth && echo "func CheckToken() {}" > internal/auth/session.go`, `git add internal/auth`.
2. [ ] `review start` → **Esperado**: `risk_level: high`, 4 lentes, y `risk_evidence` nombrando el motivo (p. ej. `"authentication in internal/auth/session.go"`).
3. [ ] Commiteen eso (`git commit -am "auth"`). Generen 1000+ líneas de texto en varios `.md`, `git add *.md`, `review start` → **Esperado**: `low`, 0 lentes. NO escala por tamaño.

### Flujo 5: La pregunta de consentimiento (necesita terminal de verdad)

1. [ ] Con un cambio tier 1/2 preparado, `review start` en terminal interactiva → **Esperado**: **dos** opciones — `1) Run the review now` / `2) Not now, just this once` — y una línea final nombrando `gentle-ai review mode disable`. **NO existe opción 3.**
2. [ ] Contesten `2` → **Esperado**: no revisa este candidato.
3. [ ] OTRO cambio y `review start` → **Esperado**: vuelve a preguntar.
4. [ ] Contesten `1` → **Esperado**: revisa, y el próximo cambio ya no pregunta.

### Flujo 6: Entrega con revisiones apagadas

1. [ ] Apaguen, hagan cambio y commit → **Esperado**: el commit funciona normal.
2. [ ] `gentle-ai review validate --gate pre-push --cwd $HOME/demo` → **Esperado**: `"delivery": "disabled/unmanaged"`, `"allowed": false`, **exit 0**. Reporta, no bloquea.
3. [ ] Verifiquen que NO diga `allow` → **Esperado**: nunca un PASS falso.

### Flujo 7: Apagar a mitad de trabajo y volver

1. [ ] Con revisiones prendidas, cambio **staged sin commitear**. Apaguen → **Esperado**: todo fluye.
2. [ ] Prendan y `review start` → **Esperado**: funciona — congela y revisa desde cero. Nada se pierde. (Si ya commitearon, el resultado trae un `hint` con `--base-ref`.)

### Flujo 8: Sin artefactos SDD fantasma

1. [ ] `git rev-parse --git-common-dir` y revisen → **Esperado**: dentro de `gentle-ai/` solo estado de review; nada de `sdd*`, `trace`, `evaluation`.

## Qué reportar

Cualquier cosa que no coincida con un **Esperado** — y lo que les resulte confuso aunque funcione. Issue con: qué intentaron, qué esperaban, qué vieron, `gentle-ai --version`, OS, y salida de terminal.

👉 https://github.com/Gentleman-Programming/gentle-ai/issues/new/choose — mencionen que es la **pre-release 2.2.0-rc.1**.

Si todo anduvo, comenten en el PR [#1801](https://github.com/Gentleman-Programming/gentle-ai/pull/1801) qué flujos pasaron y en qué plataforma — ese feedback decide el merge.

## Qué NO es un bug

- **El gate apagado sale con exit 0.** Reporta `disabled/unmanaged` pero no veta — la política del repo manda.
- **`requirements.txt`/`CMakeLists.txt` reciben una revisión (tier 1), no cero.** Un bump de dependencias sin revisar sería un downgrade de seguridad.
- **Sin terminal, la pregunta no aparece y revisa directo** (avisa por stderr). Apagar una red de seguridad en silencio no es opción.
- **"Ahora no" vuelve a preguntar en el próximo trabajo.** Por unidad de trabajo, a propósito.
- **Un `.md` con contenido ejecutable escala.** Se lee el contenido, no la extensión.
- **El `.claude/CLAUDE.md` instalado escala si lo meten al diff.** Por eso el `.gitignore` de la preparación.
