#!/usr/bin/env python3
"""
TOOL_NAME: hello_world
DESCRIPTION: A simple example addon tool that greets a user. Keep this example as a reference for writing addons.
PARAMETERS: {"type": "object", "properties": {"name": {"type": "string", "description": "The name of the person to greet."}}, "required": ["name"]}
"""

import sys
import json


def execute(args):
    """Main entry point for the tool."""
    name = args.get("name", "World")
    return f"Hello, {name}! This greeting comes from a dynamic addon tool. (Addon System Version 1.0)"


if __name__ == "__main__":
    # Read args from stdin
    input_data = sys.stdin.read()
    args = json.loads(input_data) if input_data else {}

    # Execute and print result
    try:
        result = execute(args)
        if result is not None:
            print(result)
    except Exception as e:
        print(f"Execution Error: {str(e)}")
