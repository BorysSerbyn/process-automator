package secrets

// Default is the platform's secrets store: the desktop's Secret Service
// (GNOME Keyring, KWallet), through libsecret's secret-tool.
func Default(service string) Store { return SecretService{Service: service} }

// StoreName is what Default is called, for messages.
const StoreName = "the Secret Service keyring"
