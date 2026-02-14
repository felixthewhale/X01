# X01

Autonomous AI agent written in Go.

## Features
- **Web chat**: Access the agent through a web interface.
- **Docker Sandbox**: Agent can run commands and scripts in an isolated environment.
- **SQLite Storage**:  History management.
- **Resilient Tool Execution**: Universal wrapper with hard timeouts.
- **Environment Support**: `.env` file for API keys.

## Quick Start
1. Create a `.env` file with `OPENROUTER_API_KEY=your_key`.
2. Run the executable:
   ```powershell
   .\X01.exe
   ```

## Development
- `internal/state`: High-level state management (Prime Context).
- `internal/db`: SQLite history persistence.
- `internal/core`: LLM client and execution logic.
- `internal/tools`: Tool registry and schemas.
- `internal/server`: Web interface and API.
- `internal/prompt`: Instructions.
- `internal/logger`: Logging (in terminal)+

- `cmd/agent`: Main loop entry point.
