# ncoda:shim 1
def main(xs) -> dict:
    return {"result": next((str(u) for u in (xs or []) if u and str(u).strip()), "")}
