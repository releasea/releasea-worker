package models

type RulePolicyPayload struct {
	Action    string   `json:"action"`
	AuthMode  string   `json:"authMode"`
	JWTIssuer string   `json:"jwtIssuer"`
	JWKSURI   string   `json:"jwksUri"`
	Audiences []string `json:"audiences"`
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
