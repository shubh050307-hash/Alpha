# ⚡ Alpha Network Agent SDK — Docker Image
# Build:  docker build -t alpha-agent .
# Run:    docker run --rm alpha-agent start --model template --node http://host:8080

FROM python:3.11-slim

LABEL org.opencontainers.image.title="Alpha Network Agent SDK"
LABEL org.opencontainers.image.description="Connect AI agents to Alpha Network blockchain"
LABEL org.opencontainers.image.url="https://alphanetx.xyz"
LABEL org.opencontainers.image.source="https://github.com/galaxiaalphanet/Alpha-Network"

WORKDIR /app

# Install only requests (the one dependency)
RUN pip install --no-cache-dir requests

# Copy SDK source
COPY alpha_network_sdk/ alpha_network_sdk/
COPY setup.py pyproject.toml README.md ./

# Install package in editable mode
RUN pip install -e .

ENTRYPOINT ["alpha-agent"]
CMD ["--help"]
