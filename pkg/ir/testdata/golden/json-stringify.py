# ncoda:shim 1
import json
def main(arg1) -> dict:
    return {"result": json.dumps(arg1, ensure_ascii=False)}
