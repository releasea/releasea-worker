package models

type RuleCORSPayload struct {
	Enabled       bool     `json:"enabled"`
	Origins       []string `json:"origins"`
	Methods       []string `json:"methods"`
	Headers       []string `json:"headers"`
	MaxAgeSeconds int      `json:"maxAgeSeconds"`
}

type RuleRateLimitPayload struct {
	Requests      int `json:"requests"`
	WindowSeconds int `json:"windowSeconds"`
}

type RuleCircuitBreakerPayload struct {
	ConsecutiveErrors   int `json:"consecutiveErrors"`
	IntervalSeconds     int `json:"intervalSeconds"`
	BaseEjectionSeconds int `json:"baseEjectionSeconds"`
	MaxEjectionPercent  int `json:"maxEjectionPercent"`
}

type RulePolicyPayload struct {
	Action          string                    `json:"action"`
	AuthMode        string                    `json:"authMode"`
	JWTIssuer       string                    `json:"jwtIssuer"`
	JWKSURI         string                    `json:"jwksUri"`
	Audiences       []string                  `json:"audiences"`
	RequiredScopes  []string                  `json:"requiredScopes"`
	APIKeyHeader    string                    `json:"apiKeyHeader"`
	APIKeys         []string                  `json:"apiKeys"`
	TimeoutMs       int                       `json:"timeoutMs"`
	Retries         int                       `json:"retries"`
	IPPolicy        string                    `json:"ipPolicy"`
	IPList          []string                  `json:"ipList"`
	RequiredHeaders []string                  `json:"requiredHeaders"`
	CORS            RuleCORSPayload           `json:"cors"`
	RateLimit       RuleRateLimitPayload      `json:"rateLimit"`
	CircuitBreaker  RuleCircuitBreakerPayload `json:"circuitBreaker"`
	MaxBodyBytes    int                       `json:"maxBodyBytes"`
	SecurityHeaders bool                      `json:"securityHeaders"`
	LockNetwork     bool                      `json:"lockNetwork"`
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
