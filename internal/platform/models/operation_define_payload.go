package models

type OperationMessage struct {
	OperationID   string `json:"operationId"`
	CorrelationID string `json:"correlationId,omitempty"`
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

type OperationClaimRecoveryResult struct {
	Recovered int `json:"recovered"`
	Failed    int `json:"failed"`
	Scanned   int `json:"scanned"`
}
