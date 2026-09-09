# X01

Autonomous AI agent written in Go.

## Features
- **Web chat**: Access the agent through a web interface (dashboard + reply/push API).
- **Docker Sandbox**: Agent can run commands and scripts in an isolated environment.
- **SQLite Storage**: History, memories, prime context and config persistence.
- **Resilient Tool Execution**: Universal wrapper with per-call timeouts.
- **Dynamic Addons**: Drop a Python file in `addons/` and it becomes a tool.
- **Environment Support**: `.env` file for API keys.

## Quick Start
1. Create a `.env` file with `OPENROUTER_API_KEY=your_key`.
2. Run the executable:
   ```powershell
   .\X01.exe
   ```
3. Open http://localhost:8080 for the dashboard.

## Module layout
- `cmd/agent`: Main loop entry point (heartbeat + turn loop, config defaults).
- `internal/prompt`: System prompt template.
- `internal/core`: LLM client, message sanitizing, tool executor.
- `internal/db`: SQLite persistence (history, state, memories, pending messages).
- `internal/tools`: Tool registry, schemas, Docker sandbox, addon loader.
- `internal/server`: Web dashboard, `/push`, `/reply`, `/status`, ask_user plumbing.
- `internal/logger`: Terminal logging.

> The old `internal/state` module no longer exists; prime context and config live in the
> SQLite `state` table (key/value), accessed through `internal/db`.

## Configuration
Defaults are written to the `state` table (`config` key) on first run; edit that row to
change behaviour. Recognized keys:

| Key | Default | Meaning |
| --- | --- | --- |
| `poll_interval` | `5.0` | Seconds between heartbeats. |
| `timeout` | `30.0` | Default tool timeout in seconds. |
| `max_turns` | `30` | Max LLM turns per heartbeat cycle. |
| `interactive` | `true` | `true`: a text-only reply is turned into a blocking `ask_user`. `false`: the reply ends the cycle (autonomous mode). |

`ask_user` waits 600 s by default; an explicit `timeout` argument is clamped to 30-3600 s.
`sleep` always gets a timeout longer than the requested duration.

## Writing an addon
Create `addons/my_tool.py` (repo-level addons are staged into `sandbox/addons/` so the
Docker sandbox can run them). Metadata comes from the module docstring:

```python
#!/usr/bin/env python3
"""
TOOL_NAME: my_tool
DESCRIPTION: One line describing what the tool does.
PARAMETERS: {"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}
"""

import sys, json

def execute(args):
    return f"got {args.get('x')}"

if __name__ == "__main__":
    data = sys.stdin.read()
    result = execute(json.loads(data) if data else {})
    if result is not None:
        print(result)
```

`TOOL_NAME` is required; `PARAMETERS` must be valid JSON (defaults to an empty object
schema). `sandbox/addons/*.py` wins over `addons/*.py` on name collisions, so users can
override a shipped tool locally. See `addons/hello_world.py` for a working example.

## Tests
```bash
go vet ./...
go test -race ./...
```

## Web API
| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/` | GET | Dashboard. |
| `/status` | GET | Current activity, active request, pending count. |
| `/reply` | POST | `{"content": "..."}` - answer the active `ask_user`. |
| `/push` | POST | `{"content": "..."}` - queue an external message and wake the agent. |

> **Security**: the dashboard binds `0.0.0.0:8080` and has no authentication. Run it on a
> trusted network only, or restrict the port with a firewall/reverse proxy.
