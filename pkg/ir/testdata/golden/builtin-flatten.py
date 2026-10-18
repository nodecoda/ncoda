# ncoda:shim 1
def main(xs) -> dict:
    return {"result": [str(x) for g in (xs or []) if isinstance(g, list) for x in g]}
