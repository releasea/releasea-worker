package models

type OperationMessage struct {
	OperationID string `json:"operationId"`
}

type OperationPayload struct {
	Type         string                 `json:"type"`
	ID           string                 `json:"id"`
	Status       string                 `json:"status"`
	Resource     string                 `json:"resourceId"`
	DeployID     string                 `json:"deployId"`
	RuleDeployID string                 `json:"ruleDeployId"`
	ServiceName  string                 `json:"serviceName"`
	Payload      map[string]interface{} `json:"payload"`
	CreatedAt    string                 `json:"createdAt"`
	StartedAt    string                 `json:"startedAt"`
	UpdatedAt    string                 `json:"updatedAt"`
}

type RulePolicyPayload struct {
	Action string `json:"action"`
}

type RulePayload struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ServiceID   string            `json:"serviceId"`
	Environment string            `json:"environment"`
	Hosts       []string          `json:"hosts"`
	Gateways    []string          `json:"gateways"`
	Paths       []string          `json:"paths"`
	Methods     []string          `json:"methods"`
	Protocol    string            `json:"protocol"`
	Port        int               `json:"port"`
	Status      string            `json:"status"`
	Policy      RulePolicyPayload `json:"policy"`
}

type DeploymentStrategyConfig struct {
	Type             string `json:"type"`
	CanaryPercent    int    `json:"canaryPercent"`
	BlueGreenPrimary string `json:"blueGreenPrimary"`
}

type ServicePayload struct {
	ID                      string                   `json:"id"`
	Name                    string                   `json:"name"`
	Type                    string                   `json:"type"`
	Status                  string                   `json:"status"`
	SourceType              string                   `json:"sourceType"`
	RepoURL                 string                   `json:"repoUrl"`
	Branch                  string                   `json:"branch"`
	ProjectID               string                   `json:"projectId"`
	SCMCredentialID         string                   `json:"scmCredentialId"`
	AutoDeploy              *bool                    `json:"autoDeploy"`
	PauseOnIdle             bool                     `json:"pauseOnIdle"`
	PauseIdleTimeoutSeconds int                      `json:"pauseIdleTimeoutSeconds"`
	IsActive                bool                     `json:"isActive"`
	Replicas                int                      `json:"replicas"`
	MinReplicas             int                      `json:"minReplicas"`
	MaxReplicas             int                      `json:"maxReplicas"`
	Port                    int                      `json:"port"`
	DeployTemplateID        string                   `json:"deployTemplateId"`
	DeploymentStrategy      DeploymentStrategyConfig `json:"deploymentStrategy"`
}

type ServiceConfig struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	Type               string                   `json:"type"`
	SourceType         string                   `json:"sourceType"`
	RepoURL            string                   `json:"repoUrl"`
	Branch             string                   `json:"branch"`
	RootDir            string                   `json:"rootDir"`
	DockerImage        string                   `json:"dockerImage"`
	DockerContext      string                   `json:"dockerContext"`
	DockerfilePath     string                   `json:"dockerfilePath"`
	DockerCommand      string                   `json:"dockerCommand"`
	PreDeployCommand   string                   `json:"preDeployCommand"`
	Framework          string                   `json:"framework"`
	InstallCommand     string                   `json:"installCommand"`
	BuildCommand       string                   `json:"buildCommand"`
	OutputDir          string                   `json:"outputDir"`
	CacheTTL           string                   `json:"cacheTtl"`
	ScheduleCron       string                   `json:"scheduleCron"`
	ScheduleTimezone   string                   `json:"scheduleTimezone"`
	ScheduleCommand    string                   `json:"scheduleCommand"`
	ScheduleRetries    string                   `json:"scheduleRetries"`
	ScheduleTimeout    string                   `json:"scheduleTimeout"`
	HealthCheckPath    string                   `json:"healthCheckPath"`
	Port               int                      `json:"port"`
	Replicas           int                      `json:"replicas"`
	MinReplicas        int                      `json:"minReplicas"`
	MaxReplicas        int                      `json:"maxReplicas"`
	CPU                int                      `json:"cpu"`
	Memory             int                      `json:"memory"`
	DeploymentStrategy DeploymentStrategyConfig `json:"deploymentStrategy"`
	Environment        map[string]string        `json:"environment"`
	DeployTemplateID   string                   `json:"deployTemplateId"`
	RepoManaged        bool                     `json:"repoManaged"`
}

type ScmCredential struct {
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

type DeployContext struct {
	Service        ServiceConfig       `json:"service"`
	SCM            *ScmCredential      `json:"scm"`
	Registry       *RegistryCredential `json:"registry"`
	Template       *DeployTemplate     `json:"template"`
	SecretProvider *SecretProvider     `json:"secretProvider"`
}
