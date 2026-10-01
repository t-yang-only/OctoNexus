# Octopus

Modular multi-tool automation framework. One brain, many tentacles.

Each **tentacle** is a plugin. A **pipeline** chains tentacles together with a shared context.

## Quickstart

```bash
pip install -e ".[dev]"
python -m octopus list
python -m octopus run --step hello --step shout --param name=World
octopus init my-pipeline.yaml
```

## Layout

```text
src/octopus/
  __init__.py      # public exports, version
  __main__.py      # python -m octopus entry
  plugin.py        # Plugin base + @register registry
  config.py        # YAML/JSON/TOML config load/save
  pipeline.py      # sequential pipeline executor
  cli.py           # argparse CLI
  plugins/
    __init__.py    # builtin tentacles auto-registered
    hello.py
    shout.py
tests/
```

## Write a plugin

```python
from octopus import Plugin, register

@register
class MyTool(Plugin):
    name = "mytool"
    description = "does something"

    def run(self, context: dict) -> dict:
        return {"result": context.get("input")}
```

## Pipeline file (YAML)

```yaml
params:
  name: World
steps:
  - hello
  - shout
```
