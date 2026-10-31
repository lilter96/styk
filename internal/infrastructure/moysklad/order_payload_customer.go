package moysklad

import "github.com/lilter96/styk/internal/orders"

// orderCustomerValues projects transient PII into a copy of the reviewed write.
// The durable command never carries customer values; only the dispatch-time
// command that already passed the one-use intent binding does.
func (c *OrderClient) orderCustomerValues(command orders.Command) (*orders.CustomerOrderWrite, error) {
	write := command.CustomerOrder
	if write == nil {
		return nil, orders.ErrInvalid
	}
	if !write.HasPIIMappings() {
		if len(write.TransientAttributes) > 0 || write.ShipmentAddress.IsSpecified() {
			return nil, orders.ErrInvalid
		}
		return nil, nil
	}
	if command.PII == nil {
		return nil, ErrOrderIntent
	}
	attributes, address, err := orders.ProjectCustomer(*command.PII, write.PIIMappings)
	if err != nil {
		return nil, orders.ErrInvalid
	}
	projected := *write
	projected.TransientAttributes, projected.ShipmentAddress = attributes, address
	return &projected, nil
}
