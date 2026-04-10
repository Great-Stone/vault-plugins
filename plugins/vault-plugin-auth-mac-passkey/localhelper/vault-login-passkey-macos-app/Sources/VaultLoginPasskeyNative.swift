import Foundation
import AuthenticationServices

// DEPRECATED:
// 이번 범위에서는 네이티브(AuthenticationServices) Passkey 플로우를 구현하지 않습니다.
// localhost 개발 환경에서 제약이 크고, 코드사인/엔타이틀먼트/Associated Domains까지 포함한
// “운영 도메인” 중심 설계가 필요하기 때문입니다.
//
// 대신 `SafariWebAuthnFlow` 기반으로 begin/finish를 완결합니다.

final class VaultLoginPasskeyNativeCoordinator: NSObject {
    enum Mode {
        case register
        case login
    }

    private let rpID: String
    private let mode: Mode

    init(rpID: String, mode: Mode) {
        self.rpID = rpID
        self.mode = mode
    }

    func start() {
        let provider = ASAuthorizationPlatformPublicKeyCredentialProvider(relyingPartyIdentifier: rpID)

        let request: ASAuthorizationRequest
        switch mode {
        case .register:
            // 실제 구현에서는 Vault register/begin에서 받은 challenge/user 정보를 사용해야 합니다.
            // 여기서는 구조만 보여줍니다.
            request = provider.createCredentialRegistrationRequest(challenge: Data(), name: "user", userID: Data())
        case .login:
            request = provider.createCredentialAssertionRequest(challenge: Data())
        }

        let controller = ASAuthorizationController(authorizationRequests: [request])
        controller.delegate = self
        controller.presentationContextProvider = self
        controller.performRequests()
    }
}

extension VaultLoginPasskeyNativeCoordinator: ASAuthorizationControllerDelegate {
    func authorizationController(controller: ASAuthorizationController, didCompleteWithAuthorization authorization: ASAuthorization) {
        // TODO:
        // - registration: ASAuthorizationPlatformPublicKeyCredentialRegistration
        // - assertion:     ASAuthorizationPlatformPublicKeyCredentialAssertion
        // - 결과를 Vault register/finish 또는 login/finish로 전달
        print("authorization succeeded:", authorization)
    }

    func authorizationController(controller: ASAuthorizationController, didCompleteWithError error: Error) {
        print("authorization failed:", error)
    }
}

extension VaultLoginPasskeyNativeCoordinator: ASAuthorizationControllerPresentationContextProviding {
    func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        // Xcode macOS App 프로젝트에서 window를 반환하도록 연결해야 합니다.
        return ASPresentationAnchor()
    }
}

