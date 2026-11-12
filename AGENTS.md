# sump-pump

Two Go services that watch a sump pump through a Shelly PM Mini Gen3 power meter. Commands are in the README and the `justfile`.

## Rules

- **Never run `git commit`, `git push`, or any git command that writes to or modifies repository history or remotes.** If a task requires committing or pushing, stop and tell the user to run the git command manually.
- **Never add a `Co-Authored-By` trailer or a "Generated with Claude Code" line** to commit messages or PR descriptions, including in suggested commit messages. Commits are authored by the user alone.
- **When debugging, always list every command used** - show the command, what it does, and why - so the user can learn the debugging workflow. Do this inline as you debug, not as a summary at the end.

### Pre-commit safety check

Before telling the user to commit, always run `/security-review`. It reviews the pending changes on the current branch for security issues. Once it confirms the changes are safe, offer the user a suggested commit message - do not run `git commit` yourself.

## Philosophy: Grug-Brained Development

> "Complexity very, very bad." - [grugbrain.dev](https://grugbrain.dev/)

- **Say no.** The best weapon against complexity is the word "no". No new feature, no new abstraction, until it earns its place.
- **Cheapest rung that works.** Before writing code go down the ladder and stop at the first rung that solves it - skip the feature, reuse code already here, standard library, native platform feature, a dependency already installed, one line, then build the minimum.
- **No abstraction until a pattern repeats three times.** Let cut points emerge naturally from the code; don't invent them up front.
- **80/20 solutions.** Ship 80% of the value with 20% of the code. Ugly but working beats elegant but over-engineered.
- **Chesterton's Fence.** Understand why code exists before removing it. If you don't see the use, go away and think.
- **Boring, obvious code wins.** Intermediate variables with good names beat clever one-liners. Easier to debug.
- **DRY is not a law.** A little copy-paste beats a complex abstraction built for two cases.
- **No FOLD** (Fear Of Looking Dumb). If something is too complex, say so. That's a signal to simplify, not a personal failing.

## Layout

One module, two binaries. `cmd/bridge` and `cmd/consumer` are `package main`. `internal/event` holds the NATS subjects and the message body that both import. Anything that crosses between the two binaries belongs in `internal/event`.

## Shelly meter

The bridge reconciles the meter's webhook on startup and every minute. [README → Device config](./README.md#device-config) has the env vars and the two device settings the reconciler enforces. [README → Manual recovery](./README.md#manual-recovery) has the commands for when the reconciler is off.

- **Fixed IP:** `192.168.10.188` must be a UniFi reservation for the meter's MAC (`EC:DA:3B:C6:B2:AC`). `SHELLY_ADDR` hardcodes that address. Without the reservation the meter can move to a new address on a lease renewal, and readings stop with no error.
- **Shelly app:** pairing with the app or a cloud account is optional. The meter needs only local WiFi. If app pairing fails, join the meter's `ShellyPMMiniG3-*` hotspot and set WiFi at `http://192.168.33.1`.
