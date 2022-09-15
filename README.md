# sate

Stupid state manager using tcp server.

## Basic usage

### Config

Default path config is `~/.config/sate/state.yml`, you can set path config using `-c` or `--config` flag.

```yaml
# ~/.config/sate/state.yml
host: "localhost" # default localhost
port: 9123 # default 9123
states:
    simple:
        default: "foo" # default value
    complex:
        default: "bye"
        initial: "echo replaced" # execute after parse the yaml config and store the result
        get: 'echo "state: %s"' # execute when get state and send the result
        set: "echo %s | tr a-z A-Z" # execute when set state and store the result
        onget: # execute when get state
            - "notify-send sate-get %s"
        onset: # execute when set state
            - "notify-send sate-set %s"
    counter:
        default: "23"
        dispatch:
            increases: "echo $((%s+1))"
            decreases: "echo $((%s-1))"
```

### Start server

```bash
# Start normal server with daemon.
sate start
# Start server with custom config.
sate start -c "path to config"
# Start without daemon, good for debugging.
sate start -n
```

### Get state

```bash
sate get "simple"
```

### Set state

```bash
sate set "complex" "uppercase"
```

### Dispatch state

```bash
sate dispatch "counter" "increases"
```

### Watch state changes

```bash
sate watch "simple"
```

## TODO

-   [ ] Hot reload config.
-   [x] Dispatch command.
-   [x] Support space value.

## Contribute

-   Fork.
-   Add something.
-   Describe changes in `changelog.md`.
-   Commit.
-   Push.
-   Pull request.

## Dependency

-   https://github.com/akamensky/argparse
-   https://github.com/go-yaml/yaml
