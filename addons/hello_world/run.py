import sys
import json

def main():
    try:
        # Read args from stdin
        input_data = sys.stdin.read()
        if input_data:
            args = json.loads(input_data)
        else:
            args = {}
            
        name = args.get("name", "World")
        print(f"Hello, {name}! This greeting comes from a dynamic addon tool. (Addon System Version 1.0)")
    except Exception as e:
        print(f"Error in addon: {e}")

if __name__ == "__main__":
    main()
