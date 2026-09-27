import sys


def main() -> None:
    name = sys.argv[1] if len(sys.argv) > 1 else "mini-modal"
    print(f"Hello, {name}!", flush=True)
    print("This code came from an uploaded function artifact.", flush=True)


if __name__ == "__main__":
    main()
