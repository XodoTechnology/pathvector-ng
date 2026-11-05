FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends python3-flask && rm -rf /var/lib/apt/lists/*
COPY tests/peeringdb/ /tests/peeringdb/
WORKDIR /
EXPOSE 5000
ENTRYPOINT ["python3", "/tests/peeringdb/peeringdb-test-api.py"]
