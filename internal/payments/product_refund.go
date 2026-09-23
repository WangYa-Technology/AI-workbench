package payments

// CanRefundProduct reports runtime support for the order's original provider.
// It deliberately does not use the provider selected for new checkouts.
// This is a capability projection, not a remote health check or authorization.
func (s *Service) CanRefundProduct(provider string) bool {
	if s == nil || !s.config.Enabled {
		return false
	}
	runtime, err := s.runtimes.Runtime(provider)
	return err == nil && runtimeCapabilities(runtime).Refund
}
