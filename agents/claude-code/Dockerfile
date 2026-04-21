# Daedalus Claude Code worker pod image. Built and pushed by operator;
# referenced from ProjectConfig.PluginImage.

FROM node:22-bookworm-slim

# System tools the entrypoint depends on.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git \
      jq \
      ca-certificates \
      curl \
      gnupg \
 && rm -rf /var/lib/apt/lists/*

# GitHub CLI — needed for gh pr create.
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*

# Claude Code CLI.
RUN npm install -g @anthropic-ai/claude-code

# Non-root user; needed because claude-code and npm warn/fail under root.
RUN useradd -m -s /bin/bash -u 1000 daedalus
USER daedalus
WORKDIR /home/daedalus

COPY --chown=daedalus:daedalus entrypoint.sh /usr/local/bin/daedalus-entrypoint
RUN chmod +x /usr/local/bin/daedalus-entrypoint

ENV DAEDALUS_ENVELOPE=/var/run/daedalus/envelope.json \
    DAEDALUS_MEMORY_DIR=/var/run/daedalus/memory \
    WORKSPACE=/workspace

ENTRYPOINT ["/usr/local/bin/daedalus-entrypoint"]
