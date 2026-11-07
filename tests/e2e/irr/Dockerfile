FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
COPY tests/irr/whois-stub.py /whois-stub.py
EXPOSE 43
ENTRYPOINT ["python3", "/whois-stub.py"]
