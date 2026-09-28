import os

from app import app

if __name__ == "__main__":
    import uvicorn

    for key, value in sorted(os.environ.items()):
        print(f"{key}={value}")

    uvicorn.run(app, host="0.0.0.0", port=8000)
