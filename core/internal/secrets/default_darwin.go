package secrets

// Default is the platform's secrets store: the macOS Keychain.
func Default(service string) Store { return Keychain{Service: service} }

// StoreName is what Default is called, for messages: "the macOS Keychain".
const StoreName = "the macOS Keychain"
