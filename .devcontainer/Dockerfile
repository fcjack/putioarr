FROM mcr.microsoft.com/devcontainers/go:2.1-bookworm

# Install docker-in-docker
RUN apt-get update && apt-get install -y \
    docker.io \
    docker-compose \
    && rm -rf /var/lib/apt/lists/*

# Set up the workspace
RUN mkdir -p /workspace
WORKDIR /workspace

# Set up Go environment
ENV GOPATH=/go
ENV PATH=/go/bin:$PATH

# Set up SSH
RUN mkdir -p /home/vscode/.ssh
RUN chown vscode:vscode /home/vscode/.ssh

# Switch to vscode user
USER vscode
