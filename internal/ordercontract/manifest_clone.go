package ordercontract

import "slices"

func (m Manifest) clone() Manifest {
	m.Sources = slices.Clone(m.Sources)
	m.Limitations = slices.Clone(m.Limitations)
	m.Contracts = slices.Clone(m.Contracts)
	for i, contract := range m.Contracts {
		m.Contracts[i] = contract.clone()
	}
	m.Capabilities = slices.Clone(m.Capabilities)
	for i := range m.Capabilities {
		m.Capabilities[i].Prerequisites = slices.Clone(m.Capabilities[i].Prerequisites)
		m.Capabilities[i].Sources = slices.Clone(m.Capabilities[i].Sources)
	}
	return m
}

func (c Contract) clone() Contract {
	c.Sources = slices.Clone(c.Sources)
	c.Fields = slices.Clone(c.Fields)
	for i := range c.Fields {
		c.Fields[i].Types = slices.Clone(c.Fields[i].Types)
		c.Fields[i].Sources = slices.Clone(c.Fields[i].Sources)
	}
	return c
}
