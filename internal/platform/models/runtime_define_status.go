package models

type DeploymentStatus struct {
	ObservedGeneration  int `json:"observedGeneration"`
	Replicas            int `json:"replicas"`
	UpdatedReplicas     int `json:"updatedReplicas"`
	ReadyReplicas       int `json:"readyReplicas"`
	AvailableReplicas   int `json:"availableReplicas"`
	UnavailableReplicas int `json:"unavailableReplicas"`
	Conditions          []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"conditions"`
}

type DeploymentInfo struct {
	Metadata struct {
		Generation int `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
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
