// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "vault-login-passkey-macos",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(name: "vault-login-passkey", targets: ["vault-login-passkey"])
    ],
    targets: [
        .executableTarget(
            name: "vault-login-passkey",
            path: "Sources"
        )
    ]
)

