package models

type DiscoveredWorkload struct {
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	Namespace       string   `json:"namespace"`
	Images          []string `json:"images,omitempty"`
	PrimaryImage    string   `json:"primaryImage,omitempty"`
	Ports           []int    `json:"ports,omitempty"`
	Port            int      `json:"port,omitempty"`
	Replicas        int      `json:"replicas,omitempty"`
	ScheduleCron    string   `json:"scheduleCron,omitempty"`
	HealthCheckPath string   `json:"healthCheckPath,omitempty"`
}
