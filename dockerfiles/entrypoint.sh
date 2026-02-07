#!/bin/bash
set -e

# Read environment variables
HOST_USER="${HOST_USER:-user}"
HOST_UID="${HOST_UID:-1000}"
HOST_GID="${HOST_GID:-1000}"
DISTRO_TYPE="${DISTRO_TYPE:-ubuntu}"

# Create group and user matching host UID/GID
if ! getent group "$HOST_GID" > /dev/null 2>&1; then
    groupadd -g "$HOST_GID" "$HOST_USER"
fi
HOST_GROUP=$(getent group "$HOST_GID" | cut -d: -f1)

if ! id "$HOST_USER" > /dev/null 2>&1; then
    useradd_flags="-o -u $HOST_UID -g $HOST_GID -s /bin/zsh"
    if [ -d "/home/$HOST_USER" ]; then
        useradd_flags="$useradd_flags -M -d /home/$HOST_USER"
    else
        useradd_flags="$useradd_flags -m"
    fi
    useradd $useradd_flags "$HOST_USER" 2>/dev/null || true
fi

# Add user to sudoers (passwordless)
echo "$HOST_USER ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/"$HOST_USER"
chmod 0440 /etc/sudoers.d/"$HOST_USER"

# Package persistence via /data volume
if [ -d /data ]; then
    PKG_DIR="/data/packages/$DISTRO_TYPE"
    mkdir -p "$PKG_DIR"

    case "$DISTRO_TYPE" in
        ubuntu|debian)
            for dir in /var/cache/apt /var/lib/apt /var/lib/dpkg; do
                if [ -d "$dir" ]; then
                    target="$PKG_DIR$(echo $dir | tr '/' '_')"
                    if [ ! -d "$target" ]; then
                        cp -a "$dir" "$target"
                    fi
                    rm -rf "$dir"
                    ln -sf "$target" "$dir"
                fi
            done
            ;;
        arch)
            for dir in /var/cache/pacman /var/lib/pacman; do
                if [ -d "$dir" ]; then
                    target="$PKG_DIR$(echo $dir | tr '/' '_')"
                    if [ ! -d "$target" ]; then
                        cp -a "$dir" "$target"
                    fi
                    rm -rf "$dir"
                    ln -sf "$target" "$dir"
                fi
            done
            ;;
        fedora)
            for dir in /var/cache/libdnf5 /var/lib/dnf /usr/lib/sysimage/rpm; do
                if [ -d "$dir" ]; then
                    target="$PKG_DIR$(echo $dir | tr '/' '_')"
                    if [ ! -d "$target" ]; then
                        cp -a "$dir" "$target"
                    fi
                    rm -rf "$dir"
                    ln -sf "$target" "$dir"
                fi
            done
            ;;
        alpine)
            for dir in /var/cache/apk /var/lib/apk; do
                if [ -d "$dir" ]; then
                    target="$PKG_DIR$(echo $dir | tr '/' '_')"
                    if [ ! -d "$target" ]; then
                        cp -a "$dir" "$target"
                    fi
                    rm -rf "$dir"
                    ln -sf "$target" "$dir"
                fi
            done
            ;;
    esac
fi

# Set up user's home directory
USER_HOME=$(eval echo "~$HOST_USER")

# Create .zshrc with starship init and basic config
cat > "$USER_HOME/.zshrc" << 'ZSHRC'
# History
HISTFILE=~/.zsh_history
HISTSIZE=10000
SAVEHIST=10000
setopt SHARE_HISTORY
setopt HIST_IGNORE_DUPS
setopt HIST_IGNORE_SPACE

# Basic key bindings
bindkey -e
bindkey '^[[A' up-line-or-search
bindkey '^[[B' down-line-or-search

# Tab completion
autoload -Uz compinit && compinit

# Aliases
alias ls='ls --color=auto'
alias ll='ls -lah'
alias la='ls -A'

# Starship prompt
export STARSHIP_CONFIG=/etc/starship.toml
eval "$(starship init zsh)"
ZSHRC

# Persist zsh history to /data if available
if [ -d /data ]; then
    HIST_DIR="/data/zsh_history"
    mkdir -p "$HIST_DIR"
    # Point history file to persistent storage
    sed -i "s|HISTFILE=~/.zsh_history|HISTFILE=/data/zsh_history/.zsh_history|" "$USER_HOME/.zshrc"
fi

# Set ownership
chown -R "$HOST_UID:$HOST_GID" "$USER_HOME"

# Switch to user and start zsh
exec su - "$HOST_USER" -s /bin/zsh
