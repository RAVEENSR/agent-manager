FROM python:3.11-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY main.py openapi.yaml ./
COPY probe ./probe
RUN mkdir /security-probe-fs-test && chown 65534:65534 /security-probe-fs-test

USER 65534:65534
EXPOSE 8000

CMD ["python", "main.py"]
