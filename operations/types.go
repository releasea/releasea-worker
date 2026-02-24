package operations

import (
	"releaseaworker/config"
	"releaseaworker/models"
)

type Config = config.Config

type operationMessage = models.OperationMessage
type operationPayload = models.OperationPayload

type rulePolicyPayload = models.RulePolicyPayload
type rulePayload = models.RulePayload

type deploymentStrategyConfig = models.DeploymentStrategyConfig

type servicePayload = models.ServicePayload
type serviceConfig = models.ServiceConfig

type scmCredential = models.ScmCredential
type registryCredential = models.RegistryCredential
type deployTemplate = models.DeployTemplate
type secretProvider = models.SecretProvider

type deployContext = models.DeployContext

type deploymentStatus = models.DeploymentStatus
type deploymentInfo = models.DeploymentInfo

type podList = models.PodList
type podInfo = models.PodInfo
type podMetadata = models.PodMetadata
type podStatus = models.PodStatus

type containerStatus = models.ContainerStatus
type containerState = models.ContainerState
type containerStateWaiting = models.ContainerStateWaiting
type containerStateTerminated = models.ContainerStateTerminated
