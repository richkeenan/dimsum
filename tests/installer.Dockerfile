FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl iproute2 systemd util-linux nodejs shellcheck && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . /src
CMD ["sh", "tests/install.sh"]
