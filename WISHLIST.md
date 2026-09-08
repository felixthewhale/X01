# X01 Wishlist
Review date: 2026-09-08 · Source: full codebase review (see X01-REVIEW.md at repo root)
Legend: [ ] open · [x] done

## Bugs / papercuts
- [x] Fix example Python addon so it can actually be loaded & run
      (done: flat addons/hello_world.py + LoadAddons scans repo addons/, stages to sandbox)
- [ ] Remove dead config key `config["timeout"]` (set in main.go, never read)
      — or wire it up as the default tool timeout
- [ ] Reconcile `ask_user` schema "timeout" param vs main.go forcing >= 600s
- [ ] Authenticate / restrict the web dashboard (binds 0.0.0.0, no auth):
      bind 127.0.0.1 by default or add a token for /push and /reply
- [ ] README: drop reference to nonexistent `internal/state` module;
      document actual module layout + addon format

## Design / v0.2
- [ ] Autonomy mode: config flag (e.g. `interactive`) to skip
      SynthesizeVirtualCall so text-only replies can terminate a cycle
      without blocking 10 min on ask_user
- [ ] Make `maxTurns` (30) and overall cycle duration configurable; consider
      a global heartbeat deadline (worst case today ~75 min of LLM turns)
- [ ] AskUser: replace detached stdin goroutine with a shared stdin pump so
      repeated AskUser calls don't stack blocked readers
- [ ] Moltbook: persist api_key returned by register (state/.env) so
      moltbook_* tools work without manual env setup
- [ ] Add test harness for SanitizeMessages / GetContextWindow edge cases
      (orphan tool msgs, mid-turn slicing, consecutive-role merging)
- [ ] Consider exposing `poll_interval` / port 8080 via config or CLI flags

## Notes
- The system prompt template in internal/prompt/prompt.go IS the prompt that
  ran this review session (verified first-hand by the agent instance).
