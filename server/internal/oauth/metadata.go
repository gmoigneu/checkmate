package oauth

// AuthorizationServerMetadata is the RFC 8414 discovery document.
//
// code_challenge_methods_supported is load-bearing, not decorative: the MCP spec
// tells clients to treat its absence as "this server does not support PKCE" and
// refuse to proceed, so omitting it would make the server unusable.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	ResponseModesSupported            []string `json:"response_modes_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethods     []string `json:"revocation_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`

	// RFC 9207. Advertising this is mandatory once iss is emitted, and this
	// server always emits it.
	AuthorizationResponseIssParameterSupported bool `json:"authorization_response_iss_parameter_supported"`

	// Client ID Metadata Documents, the registration mechanism the MCP draft
	// spec prefers over dynamic registration.
	ClientIDMetadataDocumentSupported bool `json:"client_id_metadata_document_supported"`

	ServiceDocumentation string `json:"service_documentation,omitempty"`
}

// Metadata builds the authorization server discovery document.
func (s *Service) Metadata() AuthorizationServerMetadata {
	meta := AuthorizationServerMetadata{
		Issuer:                            s.issuer,
		AuthorizationEndpoint:             s.issuer + "/oauth/authorize",
		TokenEndpoint:                     s.issuer + "/oauth/token",
		RevocationEndpoint:                s.issuer + "/oauth/revoke",
		ScopesSupported:                   SupportedScopes,
		ResponseTypesSupported:            []string{"code"},
		ResponseModesSupported:            []string{"query"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_basic", "client_secret_post"},
		RevocationEndpointAuthMethods:     []string{"none", "client_secret_basic", "client_secret_post"},

		// S256 only. OAuth 2.1 removed "plain", so advertising it would invite
		// a downgrade this server refuses anyway.
		CodeChallengeMethodsSupported: []string{"S256"},

		AuthorizationResponseIssParameterSupported: true,
		ClientIDMetadataDocumentSupported:          true,
	}

	if s.allowDynamicRegistration {
		meta.RegistrationEndpoint = s.issuer + "/oauth/register"
	}

	return meta
}

// ProtectedResourceMetadata is the RFC 9728 document. MCP requires the resource
// server to publish this so a client can discover which authorization server to
// talk to without being told out of band.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name,omitempty"`
	ResourceDocumentation  string   `json:"resource_documentation,omitempty"`
}

// ResourceMetadata builds the protected resource document for one resource
// identifier, which may be the canonical resource or one of its aliases.
func (s *Service) ResourceMetadata(resource string) ProtectedResourceMetadata {
	if resource == "" {
		resource = s.resource
	}

	return ProtectedResourceMetadata{
		Resource:             resource,
		AuthorizationServers: []string{s.issuer},

		// offline_access is deliberately absent: it governs refresh token
		// issuance at the authorization server and is not something the resource
		// itself requires, which is what the spec says to do.
		ScopesSupported: ResourceScopes,

		// Header only. MCP forbids tokens in the query string.
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Checkmate",
	}
}
