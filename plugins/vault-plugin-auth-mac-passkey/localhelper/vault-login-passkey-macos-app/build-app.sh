#!/usr/bin/env bash
set -euo pipefail

# Builds a runnable macOS .app bundle without Xcode.
# Output:
#   ./dist/VaultLoginPasskey.app

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
APP_NAME="VaultLoginPasskey"
APP_DIR="${DIST_DIR}/${APP_NAME}.app"
CONTENTS_DIR="${APP_DIR}/Contents"
MACOS_DIR="${CONTENTS_DIR}/MacOS"
RES_DIR="${CONTENTS_DIR}/Resources"
ICON_PNG="${ROOT_DIR}/Assets/AppIcon-1024.png"
ICONSET_DIR="${ROOT_DIR}/.build-tmp/AppIcon.iconset"
ICON_ICNS_BASENAME="AppIcon"
ICON_ICNS_NAME="${ICON_ICNS_BASENAME}.icns"
ICON_ICNS="${RES_DIR}/${ICON_ICNS_NAME}"

mkdir -p "${MACOS_DIR}" "${RES_DIR}"

mkdir -p "${ROOT_DIR}/.build-tmp"

swiftc \
  -O \
  -target arm64-apple-macosx13.0 \
  -framework AppKit \
  -framework SwiftUI \
  -framework Network \
  -o "${MACOS_DIR}/${APP_NAME}" \
  "${ROOT_DIR}/Sources/AppMain.swift" \
  "${ROOT_DIR}/Sources/ContentView.swift" \
  "${ROOT_DIR}/Sources/VaultClient.swift" \
  "${ROOT_DIR}/Sources/SafariWebAuthnFlow.swift"

if [[ -f "${ICON_PNG}" ]]; then
  rm -rf "${ICONSET_DIR}"
  mkdir -p "${ICONSET_DIR}"
  # Create the standard iconset sizes (points + @2x).
  sips -z 16 16   "${ICON_PNG}" --out "${ICONSET_DIR}/icon_16x16.png" >/dev/null
  sips -z 32 32   "${ICON_PNG}" --out "${ICONSET_DIR}/icon_16x16@2x.png" >/dev/null
  sips -z 32 32   "${ICON_PNG}" --out "${ICONSET_DIR}/icon_32x32.png" >/dev/null
  sips -z 64 64   "${ICON_PNG}" --out "${ICONSET_DIR}/icon_32x32@2x.png" >/dev/null
  sips -z 128 128 "${ICON_PNG}" --out "${ICONSET_DIR}/icon_128x128.png" >/dev/null
  sips -z 256 256 "${ICON_PNG}" --out "${ICONSET_DIR}/icon_128x128@2x.png" >/dev/null
  sips -z 256 256 "${ICON_PNG}" --out "${ICONSET_DIR}/icon_256x256.png" >/dev/null
  sips -z 512 512 "${ICON_PNG}" --out "${ICONSET_DIR}/icon_256x256@2x.png" >/dev/null
  sips -z 512 512 "${ICON_PNG}" --out "${ICONSET_DIR}/icon_512x512.png" >/dev/null
  sips -z 1024 1024 "${ICON_PNG}" --out "${ICONSET_DIR}/icon_512x512@2x.png" >/dev/null
  iconutil -c icns "${ICONSET_DIR}" -o "${ICON_ICNS}"
fi

cat > "${CONTENTS_DIR}/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>VaultLoginPasskey</string>
  <key>CFBundleIdentifier</key>
  <string>local.vault-login-passkey.app</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>VaultLoginPasskey</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>0.1</string>
  <key>CFBundleVersion</key>
  <string>2</string>
  <key>CFBundleIconFile</key>
  <string>AppIcon</string>
  <key>LSMinimumSystemVersion</key>
  <string>13.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST

echo "Built: ${APP_DIR}"
echo "Run:   open \"${APP_DIR}\""

