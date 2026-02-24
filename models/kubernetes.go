package models

type DeploymentCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type DeploymentStatus struct {
	AvailableReplicas int                   `json:"availableReplicas"`
	Replicas          int                   `json:"replicas"`
	Conditions        []DeploymentCondition `json:"conditions"`
}

type DeploymentInfo struct {
	Status DeploymentStatus `json:"status"`
}

type PodList struct {
	Items []PodInfo `json:"items"`
}

type PodInfo struct {
	Metadata PodMetadata `json:"metadata"`
	Status   PodStatus   `json:"status"`
}

type PodMetadata struct {
	Name string `json:"name"`
}

type PodStatus struct {
	Phase                 string            `json:"phase"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses"`
}

type ContainerStatus struct {
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	RestartCount int            `json:"restartCount"`
	State        ContainerState `json:"state"`
	LastState    ContainerState `json:"lastState"`
}

type ContainerState struct {
	Waiting    *ContainerStateWaiting    `json:"waiting"`
	Terminated *ContainerStateTerminated `json:"terminated"`
}

type ContainerStateWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type ContainerStateTerminated struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
	Signal   int    `json:"signal"`
}
