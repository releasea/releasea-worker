package models

type DiscoveredEnvironmentVariable struct {
	Key        string `json:"key"`
	Value      string `json:"value,omitempty"`
	SourceType string `json:"sourceType,omitempty"`
	Reference  string `json:"reference,omitempty"`
	Importable bool   `json:"importable,omitempty"`
}

type DiscoveredContainer struct {
	Name                 string                          `json:"name"`
	Image                string                          `json:"image,omitempty"`
	Ports                []int                           `json:"ports,omitempty"`
	Imported             bool                            `json:"imported,omitempty"`
	HealthCheckPath      string                          `json:"healthCheckPath,omitempty"`
	Probes               []DiscoveredProbe               `json:"probes,omitempty"`
	EnvironmentVariables []DiscoveredEnvironmentVariable `json:"environmentVariables,omitempty"`
	Command              []string                        `json:"command,omitempty"`
	Args                 []string                        `json:"args,omitempty"`
	CPUMilli             int                             `json:"cpuMilli,omitempty"`
	MemoryMi             int                             `json:"memoryMi,omitempty"`
}

type DiscoveredServiceHint struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Ports    []int  `json:"ports,omitempty"`
	Headless bool   `json:"headless,omitempty"`
}

type DiscoveredIngressHint struct {
	Name         string   `json:"name"`
	ServiceNames []string `json:"serviceNames,omitempty"`
	Hosts        []string `json:"hosts,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	TLS          bool     `json:"tls,omitempty"`
}

type DiscoveredProbe struct {
	Type          string   `json:"type"`
	Handler       string   `json:"handler"`
	ContainerName string   `json:"containerName,omitempty"`
	Path          string   `json:"path,omitempty"`
	Port          string   `json:"port,omitempty"`
	Command       []string `json:"command,omitempty"`
	Service       string   `json:"service,omitempty"`
}

type DiscoveredWorkload struct {
	Kind                 string                          `json:"kind"`
	Name                 string                          `json:"name"`
	Namespace            string                          `json:"namespace"`
	Containers           []DiscoveredContainer           `json:"containers,omitempty"`
	ServiceHints         []DiscoveredServiceHint         `json:"serviceHints,omitempty"`
	IngressHints         []DiscoveredIngressHint         `json:"ingressHints,omitempty"`
	Images               []string                        `json:"images,omitempty"`
	PrimaryImage         string                          `json:"primaryImage,omitempty"`
	Ports                []int                           `json:"ports,omitempty"`
	Port                 int                             `json:"port,omitempty"`
	Replicas             int                             `json:"replicas,omitempty"`
	ScheduleCron         string                          `json:"scheduleCron,omitempty"`
	HealthCheckPath      string                          `json:"healthCheckPath,omitempty"`
	Probes               []DiscoveredProbe               `json:"probes,omitempty"`
	EnvironmentVariables []DiscoveredEnvironmentVariable `json:"environmentVariables,omitempty"`
	Command              []string                        `json:"command,omitempty"`
	Args                 []string                        `json:"args,omitempty"`
	CPUMilli             int                             `json:"cpuMilli,omitempty"`
	MemoryMi             int                             `json:"memoryMi,omitempty"`
}
