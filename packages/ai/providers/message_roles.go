package providers

func providerMessageRoleAsUser(role string) string {
	if role == "bashExecution" || role == "custom" || role == "branchSummary" || role == "compactionSummary" {
		return "user"
	}
	return role
}
