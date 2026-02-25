package models

type DeployContext struct {
	Service        ServiceConfig       `json:"service"`
	SCM            *SCMCredential      `json:"scm"`
	Registry       *RegistryCredential `json:"registry"`
	Template       *DeployTemplate     `json:"template"`
	SecretProvider *SecretProvider     `json:"SecretProvider"`
}

type SCMCredential struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	AuthType   string `json:"authType"`
	Token      string `json:"token"`
	PrivateKey string `json:"privateKey"`
}

type RegistryCredential struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	RegistryUrl string `json:"registryUrl"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

type DeployTemplate struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Type      string                   `json:"type"`
	Resources []map[string]interface{} `json:"resources"`
}

type SecretProvider struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Type   string                 `json:"type"`
	Config map[string]interface{} `json:"config"`
}
