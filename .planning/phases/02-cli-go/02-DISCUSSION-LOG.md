# Phase 2: CLI Go - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-04-18
**Phase:** 02-cli-go
**Areas discussed:** Ciclo di vita container, Schema YAML config, Volume mount di default, Output e UX della CLI

---

## Ciclo di vita container

| Option | Description | Selected |
|--------|-------------|----------|
| Exec nel container esistente | Se il container è già running, apre una nuova sessione shell dentro quello. Un solo container attivo. | ✓ |
| Sempre nuovo container | Ogni `toolbox shell` crea un container fresh. Se uno esiste già, errore o force-replace. | |
| Ibrido: start-or-exec | Se non esiste, crea e avvia. Se esiste ma è stopped, riavvia. Se è running, exec. | |

**User's choice:** Exec nel container esistente
**Notes:** Preferito per semplicità — un solo container, più sessioni shell.

| Option | Description | Selected |
|--------|-------------|----------|
| Stop + Remove | Ferma e rimuove il container. Prossimo `toolbox shell` parte fresh. | ✓ |
| Solo stop | Ferma il container ma lo mantiene per riavviarlo dopo. | |

**User's choice:** Stop + Remove
**Notes:** Nessun dato perso perché tutto è sui volumi montati.

| Option | Description | Selected |
|--------|-------------|----------|
| Fisso 'toolbox' | Il container si chiama sempre 'toolbox'. Semplice, prevedibile. | ✓ |
| Configurabile via YAML | Nome default 'toolbox' ma sovrascrivibile in ~/.toolbox.yaml. | |

**User's choice:** Fisso 'toolbox'

---

## Schema YAML config

| Option | Description | Selected |
|--------|-------------|----------|
| Solo ~/.toolbox.yaml | Un solo posto, chiaro e prevedibile. | |
| Multi-path (home + progetto) | ~/.toolbox.yaml come global, poi .toolbox.yaml nel progetto corrente come override locale. | ✓ |

**User's choice:** Multi-path (home + progetto)
**Notes:** Lookup order: ./.toolbox.yaml (progetto) → ~/.toolbox.yaml (global) → env vars → defaults.

| Option | Description | Selected |
|--------|-------------|----------|
| Opzionale, defaults funzionano | Zero-config out of the box. | ✓ |
| Richiesto | Il file deve esistere, `toolbox init` lo genera. | |

**User's choice:** Opzionale, defaults funzionano

| Option | Description | Selected |
|--------|-------------|----------|
| Raggruppato per area | Sezioni semantiche: image, container, mounts. | ✓ |
| Flat e minimale | Chiavi semplici al primo livello. | |

**User's choice:** Raggruppato per area

---

## Volume mount di default

| Option | Description | Selected |
|--------|-------------|----------|
| ~/.claude (rw) | Settings, memory, agents, plugins GSD. | ✓ |
| ~/.gitconfig + ~/.gitconfig-dbm (ro) | Git config con email condizionale per DBM. | ✓ |
| ~/.ssh (ro) | Chiavi SSH per git push/pull. | ✓ |
| /var/run/docker.sock (rw) | Docker socket per gestire container dall'interno. | ✓ |

**User's choice:** Tutti e 4 selezionati.

| Option | Description | Selected |
|--------|-------------|----------|
| ~/.secrets mount ro di default | Montato read-only di default. | |
| Solo se configurato | Non montato di default, aggiungere in YAML se serve. | ✓ |

**User's choice:** Solo se configurato

| Option | Description | Selected |
|--------|-------------|----------|
| Warning e skip | Mostra un avviso ma avvia comunque senza quel mount. | ✓ |
| Errore e stop | Se un mount manca, la CLI si ferma. | |
| Silenzioso skip | Ignora senza dire niente. | |

**User's choice:** Warning e skip

---

## Output e UX della CLI

| Option | Description | Selected |
|--------|-------------|----------|
| Colorato con simboli | Checkmark verdi, warning gialli, errori rossi. | ✓ |
| Minimal, solo testo | Niente colori, niente simboli. | |

**User's choice:** Colorato con simboli
**Notes:** L'utente ha suggerito di aggiungere `charmbracelet/gum` come dipendenza per TUI ricco.

| Option | Description | Selected |
|--------|-------------|----------|
| Stream output Docker | Output build Docker in tempo reale. | ✓ |
| Spinner + summary | Solo spinner durante il build, summary alla fine. | |

**User's choice:** Stream output Docker

---

## Claude's Discretion

- Struttura directory Go (cmd/, internal/, pkg/)
- Dettagli Docker SDK (ContainerCreate, HostConfig)
- Gestione segnali durante exec
- Struttura interna del default config

## Deferred Ideas

- `toolbox status` — comando per stato container (future requirement CLI-06)
- `toolbox update` — pull ultima immagine da GHCR (future requirement CLI-07)
- Nome container configurabile — per ambienti paralleli futuri
