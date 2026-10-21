package models

const (
	OperationContractCatalogVersion = "v1"

	OperationStatusQueued     = "queued"
	OperationStatusInProgress = "in-progress"
	OperationStatusSucceeded  = "succeeded"
	OperationStatusFailed     = "failed"

	OperationTypeServiceDeploy          = "service.deploy"
	OperationTypeServiceScale           = "service.scale"
	OperationTypeServicePromoteCanary   = "service.promote-canary"
	OperationTypeServiceSwitchBlueGreen = "service.switch-blue-green"
	OperationTypeServiceDelete          = "service.delete"
	OperationTypeRuleDeploy             = "rule.deploy"
	OperationTypeRulePublish            = "rule.publish"
	OperationTypeRuleDelete             = "rule.delete"
	OperationTypeWorkerRestart          = "worker.restart"
)

var operationTypeCatalogOrder = []string{
	OperationTypeServiceDeploy,
	OperationTypeServiceScale,
	OperationTypeServicePromoteCanary,
	OperationTypeServiceSwitchBlueGreen,
	OperationTypeServiceDelete,
	OperationTypeRuleDeploy,
	OperationTypeRulePublish,
	OperationTypeRuleDelete,
	OperationTypeWorkerRestart,
}

var operationStatusLifecycle = []string{
	OperationStatusQueued,
	OperationStatusInProgress,
	OperationStatusSucceeded,
	OperationStatusFailed,
}

func SupportedOperationTypes() []string {
	return append([]string(nil), operationTypeCatalogOrder...)
}

func SupportedOperationStatuses() []string {
	return append([]string(nil), operationStatusLifecycle...)
}

func IsSupportedOperationType(operationType string) bool {
	for _, supported := range operationTypeCatalogOrder {
		if supported == operationType {
			return true
		}
	}
	return false
}
