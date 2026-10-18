# ncoda:shim 1
def main(xs) -> dict:
    return {"result": [{"index": i, "value": v} for i, v in enumerate(xs)]}
