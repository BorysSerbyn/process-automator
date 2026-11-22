# The image the Linux windows app (automator-tray/windows) is built in: Go,
# and for both Linux architectures the C libraries Fyne links against
# (OpenGL, X11, Wayland), with a C compiler for the one this machine isn't.
# Debian 12's glibc (2.36) keeps the binary running on any distribution
# since (it needs glibc 2.34).
ARG GO_IMAGE
FROM ${GO_IMAGE}
RUN set -eux; \
  case "$(dpkg --print-architecture)" in \
    amd64) cross=gcc-aarch64-linux-gnu ;; \
    arm64) cross=gcc-x86-64-linux-gnu ;; \
    *) echo "unsupported build machine: $(dpkg --print-architecture)" >&2; exit 1 ;; \
  esac; \
  dpkg --add-architecture amd64; dpkg --add-architecture arm64; apt-get update; \
  libs="libc6-dev libgl-dev libegl-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev"; \
  apt-get install -y --no-install-recommends pkg-config "$cross" \
    $(for arch in amd64 arm64; do for lib in $libs; do printf '%s:%s ' "$lib" "$arch"; done; done); \
  rm -rf /var/lib/apt/lists/*
