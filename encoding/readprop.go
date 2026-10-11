package encoding

import (
	"fmt"

	"github.com/anviod/bacnet/btypes"
)

func (e *Encoder) readPropertyHeader(tagPos uint8, data *btypes.PropertyData) (uint8, error) {
	// Validate data first
	if err := isValidObjectType(data.Object.ID.Type); err != nil {
		return 0, err
	}
	if err := isValidPropertyType(uint32(data.Object.Properties[0].Type)); err != nil {
		return 0, err
	}

	// Tag - Object Type and Instance
	e.contextObjectID(tagPos, data.Object.ID.Type, data.Object.ID.Instance)
	tagPos++

	// Get first property
	prop := data.Object.Properties[0]
	e.contextEnumerated(tagPos, uint32(prop.Type))
	tagPos++

	// Optional Tag - Array Index
	if prop.ArrayIndex != ArrayAll {
		e.contextUnsigned(tagPos, prop.ArrayIndex)
	}
	tagPos++
	return tagPos, nil
}

// ReadProperty is a service request to read a property that is passed.
func (e *Encoder) ReadProperty(invokeID uint8, data btypes.PropertyData) error {
	// PDU Type
	a := btypes.APDU{
		DataType:         btypes.ConfirmedServiceRequest,
		Service:          btypes.ServiceConfirmedReadProperty,
		MaxSegs:          0,
		MaxApdu:          MaxAPDU,
		InvokeId:         invokeID,
		SegmentedMessage: false,
	}
	e.APDU(a)
	e.readPropertyHeader(initialTagPos, &data)
	return e.Error()
}

// ReadPropertyAck is the response made to a ReadProperty service request.
func (e *Encoder) ReadPropertyAck(invokeID uint8, data btypes.PropertyData) error {
	if len(data.Object.Properties) != 1 {
		return fmt.Errorf("Property length length must be 1 not %d", len(data.Object.Properties))
	}
	// PDU Type
	a := btypes.APDU{
		DataType: btypes.ComplexAck,
		Service:  btypes.ServiceConfirmedReadProperty,
		MaxSegs:  0,
		MaxApdu:  MaxAPDU,
		InvokeId: invokeID,
	}
	e.APDU(a)

	tagID, err := e.readPropertyHeader(initialTagPos, &data)
	if err != nil {
		return err
	}

	// Tag 3: property value — opening and closing MUST use the same tag number
	// (ASHRAE 135 Clause 20.2.1). Incrementing between them produced 0x3E…0x4F,
	// which YABE rejects as "Didn't get response from 'Object List'".
	prop := data.Object.Properties[0]
	e.openingTag(tagID)
	e.AppData(prop.Data, false)
	e.closingTag(tagID)
	return e.Error()
}

func (d *Decoder) ReadProperty(data *btypes.PropertyData) error {
	// Must have at least 7 bytes
	if d.buff.Len() < 7 {
		return fmt.Errorf("Missing parameters")
	}

	// Tag 0: Object ID
	tag, meta := d.tagNumber()

	var expectedTag uint8
	if tag != expectedTag {
		return &ErrorIncorrectTag{expectedTag, tag}
	}
	expectedTag++

	var objectType btypes.ObjectType
	var instance btypes.ObjectInstance
	objectType, instance = d.objectId()
	data.Object.ID.Type = objectType
	data.Object.ID.Instance = instance

	// Tag 1: Property ID
	tag, meta = d.tagNumber()
	if tag != expectedTag {
		return &ErrorIncorrectTag{expectedTag, tag}
	}
	expectedTag++

	lenValue := d.value(meta)

	var prop btypes.Property
	prop.Type = btypes.PropertyType(d.enumerated(int(lenValue)))

	if d.len() != 0 {
		tag, meta = d.tagNumber()
	}

	// Check to see if we still have bytes to read.
	if d.buff.Len() != 0 || tag >= 2 {
		// If we do then that means we are reading the optional argument,
		// arra length

		// Tag 2: Array Length (OPTIONAL)
		var lenValue uint32
		lenValue = d.value(meta)

		var openTag uint8
		// I tried to not use magic numbers but it doesn't look like it can be avoided
		// If the attag we receive is a tag of 2 then set the value
		if tag == 2 {
			prop.ArrayIndex = d.unsigned(int(lenValue))
			if d.len() > 0 {
				openTag, meta = d.tagNumber()
			}
		} else {
			openTag = tag
			prop.ArrayIndex = ArrayAll
		}

		if openTag == 3 {
			// The property value is a list of application values terminated by the
			// layer-3 closing tag. Peek one byte at a time (same paradigm as
			// COVNotification) so the closing tag — not the remaining byte count —
			// is the single end condition; otherwise the closing tag and an
			// optional priority tag were parsed as application data, turning the
			// value into a polluted array.
			// 中文说明：以本层（tag 3）闭合标签为唯一结束条件，避免把闭合标签/
			// 可选 priority 标签当应用数据继续解析。
			datalist := make([]interface{}, 0)
			for d.buff.Len() > 0 {
				peek, err := d.buff.ReadByte()
				if err != nil {
					break
				}
				_ = d.buff.UnreadByte()
				// A closing tag is context-specific with the low three bits set.
				// Application values never set the class bit, so this cannot
				// false-positive on a value whose low bits happen to be 111.
				if isClosingTag(peek) && (peek&contextSpecificBit) != 0 {
					closeTag, closeMeta := d.tagNumber()
					if closeTag != 3 || !closeMeta.isClosing() {
						return fmt.Errorf("Expected closing tag 3, got tag %d", closeTag)
					}
					break
				}

				data, err := d.AppData()
				if err != nil {
					d.err = err
					return err
				}
				datalist = append(datalist, data)
			}
			prop.Data = datalist

			// If we only have one value in the list, lets just return that value
			if len(datalist) == 1 {
				prop.Data = datalist[0]
			}

			// Optional priority tag (context tag 4) may follow the value list. Read
			// it only when it is actually present and never fold it into prop.Data;
			// when absent, not a single extra byte is consumed so existing payloads
			// decode exactly as before.
			// 中文说明：仅在可选 priority 标签确实存在时读取，写入 prop.Priority，
			// 绝不并入 prop.Data；无该标签时不多读一个字节。
			if d.buff.Len() > 0 {
				peek, err := d.buff.ReadByte()
				if err != nil {
					return err
				}
				_ = d.buff.UnreadByte()
				if !isClosingTag(peek) && !isOpeningTag(peek) &&
					(peek&contextSpecificBit) != 0 && (peek>>4) == 4 {
					_, meta = d.tagNumber()
					lenValue := d.value(meta)
					prop.Priority = btypes.NPDUPriority(d.unsigned(int(lenValue)))
				}
			}
		}
	} else {
		prop.ArrayIndex = ArrayAll
	}

	// We now assemble all the values that we have read above
	data.Object.ID.Instance = instance
	data.Object.ID.Type = objectType
	data.Object.Properties = []btypes.Property{prop}

	return d.Error()
}
