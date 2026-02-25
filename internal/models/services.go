package models

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

type DeploymentStrategyConfig struct {
	Type             string `json:"type"`
	CanaryPercent    int    `json:"canaryPercent"`
	BlueGreenPrimary string `json:"blueGreenPrimary"`
}
