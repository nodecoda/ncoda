# ncoda:shim 1
def main(out) -> dict:
    return {"result": str((out[1] if len(out) > 1 else {}).get("content") or "")}
