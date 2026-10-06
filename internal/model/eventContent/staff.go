package eventContentModel

// WithoutStaffOnly is the document without its staff-only questions, as a
// participant may see it. The receiver's block list is not modified.
func (d Document) WithoutStaffOnly() Document {
	blocks := make([]Block, 0, len(d.Blocks))
	for _, block := range d.Blocks {
		if block.Type == BlockField && block.StaffOnly {
			continue
		}
		blocks = append(blocks, block)
	}
	d.Blocks = blocks
	return d
}
