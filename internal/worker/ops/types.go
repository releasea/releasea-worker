package ops

import "releaseaworker/internal/worker/config"

type Config = config.Config

type operationMessage struct {
	OperationID string `json:"operationId"`
}

type operationPayload struct {
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

type rulePolicyPayload struct {
	Action string `json:"action"`
}

type rulePayload struct {
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
	Policy      rulePolicyPayload `json:"policy"`
}

type servicePayload struct {
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
	DeploymentStrategy      deploymentStrategyConfig `json:"deploymentStrategy"`
}

type deployContext struct {
	Service        serviceConfig       `json:"service"`
	SCM            *scmCredential      `json:"scm"`
	Registry       *registryCredential `json:"registry"`
	Template       *deployTemplate     `json:"template"`
	SecretProvider *secretProvider     `json:"secretProvider"`
}

type serviceConfig struct {
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
	DeploymentStrategy deploymentStrategyConfig `json:"deploymentStrategy"`
	Environment        map[string]string        `json:"environment"`
	DeployTemplateID   string                   `json:"deployTemplateId"`
	RepoManaged        bool                     `json:"repoManaged"`
}

type deploymentStrategyConfig struct {
	Type             string `json:"type"`
	CanaryPercent    int    `json:"canaryPercent"`
	BlueGreenPrimary string `json:"blueGreenPrimary"`
}

type scmCredential struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	AuthType   string `json:"authType"`
	Token      string `json:"token"`
	PrivateKey string `json:"privateKey"`
}

type registryCredential struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	RegistryUrl string `json:"registryUrl"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

type deployTemplate struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Type      string                   `json:"type"`
	Resources []map[string]interface{} `json:"resources"`
}

type secretProvider struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Type   string                 `json:"type"`
	Config map[string]interface{} `json:"config"`
}

type deploymentStatus struct {
	AvailableReplicas int `json:"availableReplicas"`
	Replicas          int `json:"replicas"`
	Conditions        []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"conditions"`
}

type deploymentInfo struct {
	Status deploymentStatus `json:"status"`
}

type podList struct {
	Items []podInfo `json:"items"`
}

type podInfo struct {
	Metadata podMetadata `json:"metadata"`
	Status   podStatus   `json:"status"`
}

type podMetadata struct {
	Name string `json:"name"`
}

type podStatus struct {
	Phase                 string            `json:"phase"`
	ContainerStatuses     []containerStatus `json:"containerStatuses"`
	InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
}

type containerStatus struct {
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	RestartCount int            `json:"restartCount"`
	State        containerState `json:"state"`
	LastState    containerState `json:"lastState"`
}

type containerState struct {
	Waiting    *containerStateWaiting    `json:"waiting"`
	Terminated *containerStateTerminated `json:"terminated"`
}

type containerStateWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type containerStateTerminated struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
	Signal   int    `json:"signal"`
}
