from time import sleep


def greet(name: str = "mini-modal") -> str:
    print(f"Hello, {name}!", flush=True)
    print("This code came from an uploaded function artifact.", flush=True)
    sleep(10)
    return f"Hello, {name}!"


if __name__ == "__main__":
    greet()
