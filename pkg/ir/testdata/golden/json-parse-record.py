import json
def main(arg1) -> dict:
    data = json.loads(arg1)
    return {"output": {"a": data.get("a", 0), "b": data.get("b", "")}}
