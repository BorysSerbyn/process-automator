package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/BorysSerbyn/process-automator/core/pluginapi"
)

// Remote reports whether the server is reached over HTTP.
func Remote(s pluginapi.ServerSpec) bool { return s.URL != "" }

// AuthMode is the remote server's effective auth mode ("" for local servers).
func AuthMode(s pluginapi.ServerSpec) string {
	switch {
	case !Remote(s):
		return ""
	case s.Auth == "":
		return pluginapi.AuthOAuth
	default:
		return s.Auth
	}
}

// OAuth reports whether the server signs in with OAuth.
func OAuth(s pluginapi.ServerSpec) bool { return AuthMode(s) == pluginapi.AuthOAuth }

var secretRef = regexp.MustCompile(`\{([a-z0-9][a-z0-9/-]*)\}`)

// SecretKeys lists the stored secrets the server needs, in a stable order:
// those referenced in Env or Headers (OAuth tokens and client credentials are
// handled by the OAuth sign-in).
func SecretKeys(s pluginapi.ServerSpec) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]string{s.Env, s.Headers} {
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			for _, ref := range secretRef.FindAllStringSubmatch(m[k], -1) {
				if !seen[ref[1]] {
					seen[ref[1]] = true
					keys = append(keys, ref[1])
				}
			}
		}
	}
	return keys
}

// ExpandSecrets replaces {secret-key} references in v with values from get.
func ExpandSecrets(v string, get func(key string) (string, error)) (string, error) {
	var firstErr error
	out := secretRef.ReplaceAllStringFunc(v, func(ref string) string {
		val, err := get(ref[1 : len(ref)-1])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return val
	})
	return out, firstErr
}

// ValidateServer checks a server spec.
func ValidateServer(name string, s pluginapi.ServerSpec) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("server %q: "+format, append([]any{name}, args...)...)
	}
	if (s.Command == "") == (s.URL == "") {
		return bad("set exactly one of command or url")
	}
	refs := len(SecretKeys(s)) > 0
	if !Remote(s) {
		if s.Transport != "" || s.Auth != "" || len(s.Headers) > 0 || len(s.Scopes) > 0 || s.OAuthClient != nil {
			return bad("transport, auth, headers, scopes and oauthClient are for url servers")
		}
		if s.Pairing != "" && !refs {
			return bad("pairing needs a secret referenced in env")
		}
		return nil
	}
	if len(s.Args) > 0 || len(s.Env) > 0 || s.Pairing != "" || s.Dir != "" {
		return bad("args, env, dir and pairing are for command servers")
	}
	switch s.Transport {
	case "", pluginapi.TransportStreamable, pluginapi.TransportSSE:
	default:
		return bad("transport must be streamable or sse")
	}
	switch AuthMode(s) {
	case pluginapi.AuthOAuth:
		if s.Transport == pluginapi.TransportSSE {
			return bad("oauth isn't supported over sse; use auth: headers or none")
		}
		if c := s.OAuthClient; c != nil && (c.ClientIDKey == "" || c.RedirectPort <= 0) {
			return bad("oauthClient needs clientIdKey and redirectPort")
		}
		return nil
	case pluginapi.AuthHeaders:
		if !refs {
			return bad("auth: headers needs a secret referenced in headers, e.g. Authorization: \"Bearer {my-api-key}\"")
		}
	case pluginapi.AuthNone:
		if refs {
			return bad("auth: none can't reference secrets; use auth: headers")
		}
	default:
		return bad("auth must be oauth, headers or none")
	}
	if len(s.Scopes) > 0 || s.OAuthClient != nil {
		return bad("scopes and oauthClient are for auth: oauth")
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName reports whether name can name a plugin or a secret within one:
// lowercase letters, digits and dashes. Names become config keys, keychain
// keys and folder names.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// ValidatePlugins checks the plugins an app compiles in: each well-formed,
// names unique.
func ValidatePlugins(ps []pluginapi.Plugin) error {
	seen := map[string]bool{}
	var errs []error
	for _, p := range ps {
		if err := validatePlugin(p); err != nil {
			errs = append(errs, err)
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Errorf("plugin %q registered twice", p.Name))
		}
		seen[p.Name] = true
	}
	return errors.Join(errs...)
}

func validatePlugin(p pluginapi.Plugin) error {
	if !ValidName(p.Name) {
		return fmt.Errorf("plugin %q: names are lowercase letters, digits and dashes", p.Name)
	}
	switch {
	case (p.External == nil) == (p.InProcess == nil):
		return fmt.Errorf("plugin %q: set exactly one of External or InProcess", p.Name)
	case p.External != nil:
		return ValidateServer(p.Name, *p.External)
	case p.InProcess.Serve == nil:
		return fmt.Errorf("plugin %q: InProcess needs Serve", p.Name)
	}
	for _, s := range p.InProcess.Secrets {
		if !ValidName(s.Name) {
			return fmt.Errorf("plugin %q: secret %q: names are lowercase letters, digits and dashes", p.Name, s.Name)
		}
	}
	return nil
}
