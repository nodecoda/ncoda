import json
def main(arg1) -> dict:
    data = json.loads(arg1)
    return {"output": (data if isinstance(data, list) else [])}
