package jobs

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type jsonCodec[V any] struct {
	version     SchemaVersion
	root        int64
	inline      int64
	mapLike     bool
	objectBytes int64
	encodeWork  int64
	trusted     bool
	chargeErr   error
}

func JSON[V any](version SchemaVersion) Codec[V] {
	return jsonCodecFor[V](version, false)
}

func TrustedJSON[V any](version SchemaVersion) Codec[V] {
	return jsonCodecFor[V](version, true)
}

func jsonCodecFor[V any](version SchemaVersion, trusted bool) Codec[V] {
	valueType := reflect.TypeFor[V]()
	profile, err := newJSONTypeProfiler(trusted).charge(valueType)
	if !trusted && !safeJSONRuntimeSupported {
		err = fmt.Errorf("%w: safe JSON is unavailable with jsonv2", ErrInvalid)
	}
	root := int64(0)
	if uint64(valueType.Size()) > math.MaxInt64 {
		err = ErrTooLarge
	} else {
		root = int64(valueType.Size())
	}
	return jsonCodec[V]{version: version, root: root, inline: profile.maximum, mapLike: profile.mapLike, objectBytes: profile.objectBytes, encodeWork: profile.encodeWork, trusted: trusted, chargeErr: err}
}

func (this jsonCodec[V]) ID() CodecID {
	if this.trusted {
		return builtinCodecID("trusted-json")
	}
	return builtinCodecID("json")
}
func (this jsonCodec[V]) Version() SchemaVersion { return this.version }
func (this jsonCodec[V]) validateCodec() error   { return this.chargeErr }
func (this jsonCodec[V]) validateCodecLimit(limit PayloadLimit) error {
	if err := validateJSONPayloadLimit(limit); err != nil {
		return err
	}
	if this.chargeErr != nil {
		return this.chargeErr
	}
	inline := this.inline
	if inline < 16 {
		inline = 16
	}
	value, ok := multiplyTransientBytes(inline, 2)
	if !ok {
		return ErrTooLarge
	}
	minimum, ok := addTransientBytes(this.root, value)
	if !ok || minimum > int64(limit.MaxDecodedBytes) {
		return ErrTooLarge
	}
	return nil
}
func (this jsonCodec[V]) codecMode() CodecMode {
	if this.trusted {
		return TrustedCodecMode
	}
	return SafeCodecMode
}

func (this jsonCodec[V]) Encode(value V, limit PayloadLimit) ([]byte, error) {
	if err := validateJSONPayloadLimit(limit); err != nil {
		return nil, err
	}
	if this.chargeErr != nil {
		return nil, this.chargeErr
	}
	if !this.trusted {
		if err := preflightJSONEncode(reflect.ValueOf(value), limit, this.encodeWork); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("%w: JSON value cannot be encoded", ErrInvalid)
		}
		if len(encoded) > limit.MaxBytes {
			return nil, ErrTooLarge
		}
		if err := preflightJSONDecode(encoded, limit.MaxDecodedBytes, limit.MaxDepth, this.root, this.inline, this.mapLike, this.objectBytes, this.chargeErr); err != nil {
			return nil, err
		}
		return encoded, nil
	}
	remaining := limit.MaxBytes
	if remaining < math.MaxInt {
		remaining++
	}
	buffer := &limitedBuffer{remaining: remaining}
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(value); err != nil {
		if buffer.exceeded {
			return nil, ErrTooLarge
		}
		return nil, fmt.Errorf("%w: trusted JSON value cannot be encoded", ErrInvalid)
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	if len(encoded) > limit.MaxBytes {
		return nil, ErrTooLarge
	}
	if err := preflightJSONDecode(encoded, limit.MaxDecodedBytes, limit.MaxDepth, this.root, this.inline, this.mapLike, this.objectBytes, this.chargeErr); err != nil {
		return nil, err
	}
	return bytes.Clone(encoded), nil
}

func (this jsonCodec[V]) Decode(encoded []byte, limit PayloadLimit) (V, error) {
	var value V
	if err := validateJSONPayloadLimit(limit); err != nil {
		return value, err
	}
	if this.chargeErr != nil {
		return value, this.chargeErr
	}
	if len(encoded) > limit.MaxBytes {
		return value, ErrTooLarge
	}
	if err := preflightJSONDecode(encoded, limit.MaxDecodedBytes, limit.MaxDepth, this.root, this.inline, this.mapLike, this.objectBytes, this.chargeErr); err != nil {
		return value, err
	}
	if err := json.Unmarshal(encoded, &value); err != nil {
		return value, fmt.Errorf("%w: JSON payload is malformed", ErrCorrupt)
	}
	return value, nil
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (this *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > this.remaining {
		this.exceeded = true
		return 0, ErrTooLarge
	}
	n, err := this.Buffer.Write(data)
	this.remaining -= n
	return n, err
}

type jsonEncodeIdentity struct {
	value reflect.Type
	data  uintptr
}

type jsonEncodePreflight struct {
	maximum     int64
	total       int64
	workMaximum int64
	work        int64
	depth       int
	traversal   int
	active      map[jsonEncodeIdentity]struct{}
}

const (
	jsonEncodeWalkerBytes    int64 = 256
	jsonMapSortEntryBytes    int64 = 128
	jsonTypeWorkUnitBytes    int64 = 8
	jsonTypeWorkTextFactor   int64 = 2
	jsonSafeRuntimeBytes     int64 = 16 << 20
	jsonTypeMaximumDepth           = 1024
	jsonTypeMaximumNodes           = 1024
	jsonTypeMaximumEdges           = 4096
	jsonTypeMaximumTextBytes       = 1 << 20
)

func preflightJSONEncode(value reflect.Value, limit PayloadLimit, staticWork int64) error {
	maximum := limit.MaxBytes
	traversal, ok := multiplyTransientBytes(int64(limit.MaxDepth), 8)
	if !ok {
		return ErrTooLarge
	}
	traversal, ok = addTransientBytes(traversal, 32)
	if !ok || traversal > math.MaxInt {
		return ErrTooLarge
	}
	if traversal > jsonTypeMaximumDepth {
		traversal = jsonTypeMaximumDepth
	}
	preflight := jsonEncodePreflight{
		maximum:     int64(maximum),
		workMaximum: jsonSafeRuntimeBytes,
		depth:       limit.MaxDepth,
		traversal:   int(traversal),
		active:      make(map[jsonEncodeIdentity]struct{}),
	}
	if err := preflight.addWork(staticWork); err != nil {
		return err
	}
	return preflight.value(value, 0, 0)
}

func (this *jsonEncodePreflight) add(value int64) error {
	if value < 0 || value > this.maximum-this.total {
		return ErrTooLarge
	}
	this.total += value
	return nil
}

func (this *jsonEncodePreflight) addWork(value int64) error {
	if value < 0 || value > this.workMaximum-this.work {
		return ErrTooLarge
	}
	this.work += value
	return nil
}

func (this *jsonEncodePreflight) value(value reflect.Value, depth, traversal int) error {
	if traversal > this.traversal {
		return ErrTooLarge
	}
	if err := this.addWork(jsonEncodeWalkerBytes); err != nil {
		return err
	}
	if !value.IsValid() {
		return this.add(4)
	}
	if jsonUnsafeHook(value.Type()) {
		return ErrInvalid
	}
	if value.Type() == reflect.TypeFor[json.Number]() {
		return this.add(int64(max(1, value.Len())))
	}
	switch value.Kind() {
	case reflect.Interface:
		return ErrInvalid
	case reflect.Pointer:
		if value.IsNil() {
			return this.add(4)
		}
		identity := jsonEncodeIdentity{value: value.Type(), data: value.Pointer()}
		if _, ok := this.active[identity]; ok {
			return ErrInvalid
		}
		this.active[identity] = struct{}{}
		defer delete(this.active, identity)
		return this.value(value.Elem(), depth, traversal+1)
	case reflect.Bool:
		if value.Bool() {
			return this.add(4)
		}
		return this.add(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var encoded [32]byte
		return this.add(int64(len(strconv.AppendInt(encoded[:0], value.Int(), 10))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var encoded [32]byte
		return this.add(int64(len(strconv.AppendUint(encoded[:0], value.Uint(), 10))))
	case reflect.Float32, reflect.Float64:
		floating := value.Float()
		if math.IsInf(floating, 0) || math.IsNaN(floating) {
			return ErrInvalid
		}
		return this.add(32)
	case reflect.String:
		if value.Type() == reflect.TypeFor[json.Number]() && value.Len() == 0 {
			return this.add(1)
		}
		return this.string(value.String())
	case reflect.Slice:
		if value.IsNil() {
			return this.add(4)
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if jsonUnsafeHook(value.Type().Elem()) {
				return ErrInvalid
			}
			length, ok := addTransientBytes(int64(value.Len()), 2)
			if !ok {
				return ErrTooLarge
			}
			length = length / 3
			length, ok = multiplyTransientBytes(length, 4)
			if !ok {
				return ErrTooLarge
			}
			length, ok = addTransientBytes(length, 2)
			if !ok {
				return ErrTooLarge
			}
			return this.add(length)
		}
		return this.sequence(value, depth, traversal)
	case reflect.Array:
		return this.sequence(value, depth, traversal)
	case reflect.Map:
		if value.IsNil() {
			return this.add(4)
		}
		return this.object(value, depth, traversal)
	case reflect.Struct:
		return this.structure(value, depth, traversal)
	default:
		return ErrInvalid
	}
}

func (this *jsonEncodePreflight) sequence(value reflect.Value, depth, traversal int) error {
	depth++
	if depth > this.depth {
		return ErrTooLarge
	}
	identity := jsonEncodeIdentity{}
	if value.Kind() == reflect.Slice {
		identity = jsonEncodeIdentity{value: value.Type(), data: value.Pointer()}
		if _, ok := this.active[identity]; ok {
			return ErrInvalid
		}
		this.active[identity] = struct{}{}
		defer delete(this.active, identity)
	}
	if err := this.add(2); err != nil {
		return err
	}
	for index := 0; index < value.Len(); index++ {
		if index > 0 {
			if err := this.add(1); err != nil {
				return err
			}
		}
		if err := this.value(value.Index(index), depth, traversal+1); err != nil {
			return err
		}
	}
	return nil
}

func (this *jsonEncodePreflight) object(value reflect.Value, depth, traversal int) error {
	depth++
	if depth > this.depth {
		return ErrTooLarge
	}
	identity := jsonEncodeIdentity{value: value.Type(), data: value.Pointer()}
	if _, ok := this.active[identity]; ok {
		return ErrInvalid
	}
	this.active[identity] = struct{}{}
	defer delete(this.active, identity)
	if err := this.add(2); err != nil {
		return err
	}
	iterator := value.MapRange()
	entries := 0
	for iterator.Next() {
		if err := this.addWork(jsonMapSortEntryBytes); err != nil {
			return err
		}
		if entries > 0 {
			if err := this.add(1); err != nil {
				return err
			}
		}
		entries++
		if err := this.mapKey(iterator.Key()); err != nil {
			return err
		}
		if err := this.add(1); err != nil {
			return err
		}
		if err := this.value(iterator.Value(), depth, traversal+1); err != nil {
			return err
		}
	}
	return nil
}

func (this *jsonEncodePreflight) mapKey(value reflect.Value) error {
	if jsonUnsafeHook(value.Type()) {
		return ErrInvalid
	}
	switch value.Kind() {
	case reflect.String:
		return this.string(value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var encoded [32]byte
		return this.add(int64(len(strconv.AppendInt(encoded[:0], value.Int(), 10))) + 2)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var encoded [32]byte
		return this.add(int64(len(strconv.AppendUint(encoded[:0], value.Uint(), 10))) + 2)
	default:
		return ErrInvalid
	}
}

func (this *jsonEncodePreflight) structure(value reflect.Value, depth, traversal int) error {
	depth++
	if depth > this.depth {
		return ErrTooLarge
	}
	if err := this.add(2); err != nil {
		return err
	}
	fields := 0
	for index := 0; index < value.NumField(); index++ {
		if err := this.addWork(jsonEncodeWalkerBytes); err != nil {
			return err
		}
		fieldType := value.Type().Field(index)
		if !fieldType.IsExported() && !fieldType.Anonymous {
			continue
		}
		rawTag := fieldType.Tag.Get("json")
		if rawTag == "-" {
			continue
		}
		name, options, _ := strings.Cut(rawTag, ",")
		if fields > 0 {
			if err := this.add(1); err != nil {
				return err
			}
		}
		fields++
		nameBytes, ok := jsonStringBytes(name)
		if !ok {
			return ErrTooLarge
		}
		fallbackBytes, ok := jsonStringBytes(fieldType.Name)
		if !ok {
			return ErrTooLarge
		}
		if name == "" {
			nameBytes = fallbackBytes
		} else if !validJSONTagName(name) {
			nameBytes = max(nameBytes, fallbackBytes)
		}
		if err := this.add(nameBytes); err != nil {
			return err
		}
		if err := this.add(1); err != nil {
			return err
		}
		before := this.total
		if err := this.value(value.Field(index), depth, traversal+1); err != nil {
			return err
		}
		if jsonTagOption(options, "string") {
			if err := this.add(this.total - before + 2); err != nil {
				return err
			}
		}
	}
	return nil
}

func (this *jsonEncodePreflight) string(value string) error {
	charge, _, ok := jsonStringBytesLimited(value, this.maximum-this.total)
	if !ok {
		return ErrTooLarge
	}
	return this.add(charge)
}

func jsonStringBytes(value string) (int64, bool) {
	charge, _, ok := jsonStringBytesLimited(value, math.MaxInt64)
	return charge, ok
}

func jsonStringBytesLimited(value string, maximum int64) (int64, int64, bool) {
	length := int64(len(value))
	if maximum < 2 || length > maximum-2 {
		return 0, 0, false
	}
	charge := int64(2)
	scanned := int64(0)
	add := func(value int64) bool {
		var ok bool
		charge, ok = addTransientBytes(charge, value)
		return ok && charge <= maximum
	}
	for len(value) > 0 {
		if value[0] < utf8.RuneSelf {
			character := value[0]
			value = value[1:]
			scanned++
			switch {
			case character < 0x20:
				if !add(6) {
					return 0, scanned, false
				}
			case character == '\\' || character == '"':
				if !add(2) {
					return 0, scanned, false
				}
			case character == '<' || character == '>' || character == '&':
				if !add(6) {
					return 0, scanned, false
				}
			default:
				if !add(1) {
					return 0, scanned, false
				}
			}
			continue
		}
		character, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		scanned += int64(size)
		charge := int64(size)
		if character == utf8.RuneError && size == 1 || character == '\u2028' || character == '\u2029' {
			charge = 6
		}
		if !add(charge) {
			return 0, scanned, false
		}
	}
	return charge, scanned, true
}

func jsonTagOption(options, want string) bool {
	for options != "" {
		var option string
		option, options, _ = strings.Cut(options, ",")
		if option == want {
			return true
		}
	}
	return false
}

func validJSONTagName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", character) {
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

const (
	jsonMapHeaderControlBytes int64 = 256
	jsonMapGroupSlots         int64 = 8
	jsonMapDirectSlotBytes    int64 = 128
	jsonMapEntryBytes         int64 = 128
	jsonSafeMaximumDepth            = 1024
)

type jsonDecodeCharge struct {
	maximum  int64
	total    int64
	value    int64
	mapLike  bool
	object   int64
	mapEntry int64
}

func preflightJSONDecode(encoded []byte, maximum, depth int, root, inline int64, mapLike bool, objectBytes int64, profileErr error) error {
	if maximum <= 0 {
		return ErrInvalid
	}
	if profileErr != nil {
		return profileErr
	}
	if inline < 16 {
		inline = 16
	}
	valueCharge, ok := multiplyTransientBytes(inline, 2)
	if !ok {
		return ErrTooLarge
	}
	charge := &jsonDecodeCharge{maximum: int64(maximum), total: root, value: valueCharge, mapLike: mapLike, object: objectBytes, mapEntry: jsonMapEntryBytes}
	if root < 0 || root > int64(maximum) {
		return ErrTooLarge
	}
	return scanJSON(encoded, depth, charge)
}

func (this *jsonDecodeCharge) add(value int64) error {
	if value < 0 || value > this.maximum-this.total {
		return ErrTooLarge
	}
	this.total += value
	return nil
}

func (this *jsonDecodeCharge) addString(raw, decoded int64, key bool) error {
	value, ok := addTransientBytes(raw, decoded)
	if !ok {
		return ErrTooLarge
	}
	if key {
		value, ok = addTransientBytes(value, decoded)
		if !ok {
			return ErrTooLarge
		}
		if this.mapLike {
			value, ok = addTransientBytes(value, decoded)
			if !ok {
				return ErrTooLarge
			}
			value, ok = addTransientBytes(value, this.mapEntry)
			if !ok {
				return ErrTooLarge
			}
		}
	}
	return this.add(value)
}

func scanJSON(encoded []byte, maximumDepth int, charge *jsonDecodeCharge) error {
	if maximumDepth <= 0 {
		return ErrInvalid
	}
	if maximumDepth > jsonSafeMaximumDepth {
		maximumDepth = jsonSafeMaximumDepth
	}
	var frames [jsonSafeMaximumDepth]uint8
	depth := 0
	index := 0
	root := false
	for {
		index = skipJSONSpace(encoded, index)
		if depth == 0 {
			if root {
				if index == len(encoded) {
					return nil
				}
				return ErrCorrupt
			}
			next, opener, err := scanJSONValue(encoded, index, charge)
			if err != nil {
				return err
			}
			root = true
			index = next
			if opener != 0 {
				if depth == maximumDepth {
					return ErrTooLarge
				}
				frames[depth] = opener
				depth++
			}
			continue
		}
		frame := &frames[depth-1]
		if *frame&0xc0 == 0x40 {
			switch *frame & 0x3f {
			case 0, 2:
				if *frame&0x3f == 0 && index < len(encoded) && encoded[index] == ']' {
					depth--
					index++
					continue
				}
				next, opener, err := scanJSONValue(encoded, index, charge)
				if err != nil {
					return err
				}
				*frame = 0x41
				index = next
				if opener != 0 {
					if depth == maximumDepth {
						return ErrTooLarge
					}
					frames[depth] = opener
					depth++
				}
			case 1:
				if index >= len(encoded) {
					return ErrCorrupt
				}
				switch encoded[index] {
				case ',':
					*frame = 0x42
					index++
				case ']':
					depth--
					index++
				default:
					return ErrCorrupt
				}
			}
			continue
		}
		switch *frame & 0x3f {
		case 0, 4:
			if *frame&0x3f == 0 && index < len(encoded) && encoded[index] == '}' {
				depth--
				index++
				continue
			}
			next, raw, decoded, err := scanJSONString(encoded, index)
			if err != nil {
				return err
			}
			if charge != nil {
				if err := charge.addString(raw, decoded, true); err != nil {
					return err
				}
			}
			*frame = 0x81
			index = next
		case 1:
			if index >= len(encoded) || encoded[index] != ':' {
				return ErrCorrupt
			}
			*frame = 0x82
			index++
		case 2:
			next, opener, err := scanJSONValue(encoded, index, charge)
			if err != nil {
				return err
			}
			*frame = 0x83
			index = next
			if opener != 0 {
				if depth == maximumDepth {
					return ErrTooLarge
				}
				frames[depth] = opener
				depth++
			}
		case 3:
			if index >= len(encoded) {
				return ErrCorrupt
			}
			switch encoded[index] {
			case ',':
				*frame = 0x84
				index++
			case '}':
				depth--
				index++
			default:
				return ErrCorrupt
			}
		}
	}
}

func scanJSONValue(encoded []byte, index int, charge *jsonDecodeCharge) (int, uint8, error) {
	index = skipJSONSpace(encoded, index)
	if index >= len(encoded) {
		return 0, 0, ErrCorrupt
	}
	if charge != nil {
		if err := charge.add(charge.value); err != nil {
			return 0, 0, err
		}
	}
	switch encoded[index] {
	case '{':
		if charge != nil && charge.mapLike {
			if err := charge.add(charge.object); err != nil {
				return 0, 0, err
			}
		}
		return index + 1, 0x80, nil
	case '[':
		return index + 1, 0x40, nil
	case '"':
		next, raw, decoded, err := scanJSONString(encoded, index)
		if err != nil {
			return 0, 0, err
		}
		if charge != nil {
			if err := charge.addString(raw, decoded, false); err != nil {
				return 0, 0, err
			}
		}
		return next, 0, nil
	case 't':
		return scanJSONLiteral(encoded, index, "true")
	case 'f':
		return scanJSONLiteral(encoded, index, "false")
	case 'n':
		return scanJSONLiteral(encoded, index, "null")
	default:
		next, ok := scanJSONNumber(encoded, index)
		if !ok {
			return 0, 0, ErrCorrupt
		}
		if charge != nil {
			if err := charge.add(int64(next - index)); err != nil {
				return 0, 0, err
			}
		}
		return next, 0, nil
	}
}

func scanJSONLiteral(encoded []byte, index int, literal string) (int, uint8, error) {
	if len(encoded)-index < len(literal) {
		return 0, 0, ErrCorrupt
	}
	for offset := range len(literal) {
		if encoded[index+offset] != literal[offset] {
			return 0, 0, ErrCorrupt
		}
	}
	return index + len(literal), 0, nil
}

func scanJSONString(encoded []byte, index int) (int, int64, int64, error) {
	if index >= len(encoded) || encoded[index] != '"' {
		return 0, 0, 0, ErrCorrupt
	}
	start := index
	decoded := int64(0)
	for index++; index < len(encoded); {
		value := encoded[index]
		if value == '"' {
			raw, ok := addTransientBytes(int64(index+1-start), 2*utf8.UTFMax)
			if !ok {
				return 0, 0, 0, ErrTooLarge
			}
			return index + 1, raw, decoded, nil
		}
		if value < 0x20 {
			return 0, 0, 0, ErrCorrupt
		}
		if value == '\\' {
			index++
			if index >= len(encoded) {
				return 0, 0, 0, ErrCorrupt
			}
			switch encoded[index] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				decoded++
				index++
				continue
			case 'u':
				code, ok := scanJSONHex(encoded, index+1)
				if !ok {
					return 0, 0, 0, ErrCorrupt
				}
				index += 5
				addition := int64(3)
				if code >= 0xd800 && code <= 0xdbff && index+6 <= len(encoded) && encoded[index] == '\\' && encoded[index+1] == 'u' {
					low, valid := scanJSONHex(encoded, index+2)
					if valid && low >= 0xdc00 && low <= 0xdfff {
						addition = 4
						index += 6
					}
				} else if code < 0xd800 || code > 0xdfff {
					addition = int64(utf8.RuneLen(rune(code)))
				}
				var added bool
				decoded, added = addTransientBytes(decoded, addition)
				if !added {
					return 0, 0, 0, ErrTooLarge
				}
				continue
			default:
				return 0, 0, 0, ErrCorrupt
			}
		}
		character, size := utf8.DecodeRune(encoded[index:])
		if character == utf8.RuneError && size == 1 {
			return 0, 0, 0, ErrCorrupt
		}
		addition := int64(size)
		var ok bool
		decoded, ok = addTransientBytes(decoded, addition)
		if !ok {
			return 0, 0, 0, ErrTooLarge
		}
		index += size
	}
	return 0, 0, 0, ErrCorrupt
}

func scanJSONHex(encoded []byte, index int) (uint16, bool) {
	if len(encoded)-index < 4 {
		return 0, false
	}
	value := uint16(0)
	for _, digit := range encoded[index : index+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func scanJSONNumber(encoded []byte, index int) (int, bool) {
	start := index
	if index < len(encoded) && encoded[index] == '-' {
		index++
	}
	if index >= len(encoded) {
		return 0, false
	}
	if encoded[index] == '0' {
		index++
	} else {
		if encoded[index] < '1' || encoded[index] > '9' {
			return 0, false
		}
		for index < len(encoded) && encoded[index] >= '0' && encoded[index] <= '9' {
			index++
		}
	}
	if index < len(encoded) && encoded[index] == '.' {
		index++
		first := index
		for index < len(encoded) && encoded[index] >= '0' && encoded[index] <= '9' {
			index++
		}
		if index == first {
			return 0, false
		}
	}
	if index < len(encoded) && (encoded[index] == 'e' || encoded[index] == 'E') {
		index++
		if index < len(encoded) && (encoded[index] == '+' || encoded[index] == '-') {
			index++
		}
		first := index
		for index < len(encoded) && encoded[index] >= '0' && encoded[index] <= '9' {
			index++
		}
		if index == first {
			return 0, false
		}
	}
	return index, index > start
}

func skipJSONSpace(encoded []byte, index int) int {
	for index < len(encoded) {
		switch encoded[index] {
		case ' ', '\t', '\r', '\n':
			index++
		default:
			return index
		}
	}
	return index
}

func jsonMapObjectCharge(key, element reflect.Type) (int64, error) {
	keyBytes, err := jsonMapSlotCharge(key)
	if err != nil {
		return 0, err
	}
	elementBytes, err := jsonMapSlotCharge(element)
	if err != nil {
		return 0, err
	}
	perSlot, ok := addTransientBytes(keyBytes, elementBytes)
	if !ok {
		return 0, ErrTooLarge
	}
	group, ok := multiplyTransientBytes(perSlot, jsonMapGroupSlots)
	if !ok {
		return 0, ErrTooLarge
	}
	total, ok := addTransientBytes(jsonMapHeaderControlBytes, group)
	if !ok {
		return 0, ErrTooLarge
	}
	return total, nil
}

func jsonMapSlotCharge(value reflect.Type) (int64, error) {
	size := value.Size()
	if uint64(size) > math.MaxInt64 {
		return 0, ErrTooLarge
	}
	if int64(size) <= jsonMapDirectSlotBytes {
		return int64(size), nil
	}
	pointer := reflect.TypeFor[*byte]().Size()
	if uint64(pointer) > math.MaxInt64 {
		return 0, ErrTooLarge
	}
	return int64(pointer), nil
}

type jsonTypeProfile struct {
	maximum     int64
	mapLike     bool
	objectBytes int64
	encodeWork  int64
}

type jsonTypeEdge struct {
	target     int
	cumulative bool
}

type jsonTypeNode struct {
	size        int64
	mapLike     bool
	objectBytes int64
	edges       []jsonTypeEdge
}

type jsonTypeProfiler struct {
	trusted     bool
	indexes     map[reflect.Type]int
	nodes       []jsonTypeNode
	workMaximum int64
	workUnits   int64
	workText    int64
	visits      int
	hookVisits  int
	edges       int
	components  int
}

func newJSONTypeProfiler(trusted bool) *jsonTypeProfiler {
	return &jsonTypeProfiler{
		trusted:     trusted,
		indexes:     make(map[reflect.Type]int),
		workMaximum: jsonSafeRuntimeBytes,
	}
}

func (this *jsonTypeProfiler) charge(value reflect.Type) (jsonTypeProfile, error) {
	if value == nil {
		return jsonTypeProfile{}, ErrInvalid
	}
	if _, err := this.discover(value, 0); err != nil {
		return jsonTypeProfile{}, err
	}
	return this.profile()
}

func (this *jsonTypeProfiler) discover(value reflect.Type, depth int) (int, error) {
	if index, ok := this.indexes[value]; ok {
		return index, nil
	}
	if depth > jsonTypeMaximumDepth {
		return 0, ErrTooLarge
	}
	if len(this.nodes) >= jsonTypeMaximumNodes {
		return 0, ErrTooLarge
	}
	if !this.trusted {
		this.hookVisits++
		if jsonUnsafeHook(value) {
			return 0, fmt.Errorf("%w: custom JSON coding requires TrustedJSON", ErrInvalid)
		}
		if err := validateSafeJSONType(value); err != nil {
			return 0, err
		}
	}
	if err := this.addTypeWork(1, 0); err != nil {
		return 0, err
	}
	size := value.Size()
	if uint64(size) > math.MaxInt64 {
		return 0, ErrTooLarge
	}
	index := len(this.nodes)
	this.indexes[value] = index
	this.nodes = append(this.nodes, jsonTypeNode{})
	this.visits++
	node := jsonTypeNode{size: int64(size), mapLike: value.Kind() == reflect.Map || value.Kind() == reflect.Interface}
	if value.Kind() == reflect.Map {
		objectBytes, err := jsonMapObjectCharge(value.Key(), value.Elem())
		if err != nil {
			return 0, err
		}
		node.objectBytes = objectBytes
	} else if value.Kind() == reflect.Interface {
		objectBytes, err := jsonMapObjectCharge(reflect.TypeFor[string](), reflect.TypeFor[any]())
		if err != nil {
			return 0, err
		}
		node.objectBytes = objectBytes
	}
	visit := func(child reflect.Type, cumulative bool) error {
		if this.edges >= jsonTypeMaximumEdges {
			return ErrTooLarge
		}
		this.edges++
		target, err := this.discover(child, depth+1)
		if err != nil {
			return err
		}
		node.edges = append(node.edges, jsonTypeEdge{target: target, cumulative: cumulative})
		return nil
	}
	switch value.Kind() {
	case reflect.Array, reflect.Slice:
		if err := visit(value.Elem(), false); err != nil {
			return 0, err
		}
	case reflect.Pointer:
		if err := visit(value.Elem(), true); err != nil {
			return 0, err
		}
	case reflect.Map:
		if err := visit(value.Key(), false); err != nil {
			return 0, err
		}
		if err := visit(value.Elem(), false); err != nil {
			return 0, err
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			fieldText, ok := addTransientBytes(int64(len(field.Name)), int64(len(field.Tag)))
			if !ok {
				return 0, ErrTooLarge
			}
			if err := this.addTypeWork(1, fieldText); err != nil {
				return 0, err
			}
			if field.IsExported() || field.Anonymous {
				rawTag := field.Tag.Get("json")
				if rawTag == "-" {
					continue
				}
				if field.Anonymous && !field.IsExported() && field.Type.Kind() == reflect.Pointer {
					return 0, fmt.Errorf("%w: unexported anonymous JSON pointer requires an explicit exclusion", ErrInvalid)
				}
				_, options, _ := strings.Cut(rawTag, ",")
				if !this.trusted && jsonTagOption(options, "omitzero") && jsonIsZeroHook(field.Type) {
					return 0, fmt.Errorf("%w: custom IsZero requires TrustedJSON", ErrInvalid)
				}
				promoted := field.Anonymous && (field.Type.Kind() == reflect.Struct ||
					(field.Type.Kind() == reflect.Pointer && field.Type.Elem().Kind() == reflect.Struct))
				if err := visit(field.Type, promoted); err != nil {
					return 0, err
				}
			}
		}
	}
	this.nodes[index] = node
	return index, nil
}

func (this *jsonTypeProfiler) addTypeWork(units, text int64) error {
	if units < 0 || text < 0 || text > jsonTypeMaximumTextBytes || this.workText > jsonTypeMaximumTextBytes-text {
		return ErrTooLarge
	}
	workUnits, ok := addTransientBytes(this.workUnits, units)
	if !ok {
		return ErrTooLarge
	}
	workText, ok := addTransientBytes(this.workText, text)
	if !ok {
		return ErrTooLarge
	}
	squared, ok := multiplyTransientBytes(workUnits, workUnits)
	if !ok {
		return ErrTooLarge
	}
	work, ok := multiplyTransientBytes(squared, jsonTypeWorkUnitBytes)
	if !ok {
		return ErrTooLarge
	}
	textWork, ok := multiplyTransientBytes(workText, jsonTypeWorkTextFactor)
	if !ok {
		return ErrTooLarge
	}
	work, ok = addTransientBytes(work, textWork)
	if !ok || work > this.workMaximum {
		return ErrTooLarge
	}
	this.workUnits = workUnits
	this.workText = workText
	return nil
}

func (this *jsonTypeProfiler) typeWork() (int64, error) {
	squared, ok := multiplyTransientBytes(this.workUnits, this.workUnits)
	if !ok {
		return 0, ErrTooLarge
	}
	work, ok := multiplyTransientBytes(squared, jsonTypeWorkUnitBytes)
	if !ok {
		return 0, ErrTooLarge
	}
	textWork, ok := multiplyTransientBytes(this.workText, jsonTypeWorkTextFactor)
	if !ok {
		return 0, ErrTooLarge
	}
	work, ok = addTransientBytes(work, textWork)
	if !ok || work > this.workMaximum {
		return 0, ErrTooLarge
	}
	return work, nil
}

func (this *jsonTypeProfiler) profile() (jsonTypeProfile, error) {
	encodeWork, err := this.typeWork()
	if err != nil {
		return jsonTypeProfile{}, err
	}
	componentOf, count := this.cumulativeComponents()
	this.components = count
	weights := make([]int64, count)
	edges := make([][]int, count)
	mapLike := false
	objectBytes := int64(0)
	for index, node := range this.nodes {
		component := componentOf[index]
		weight, ok := addTransientBytes(weights[component], node.size)
		if !ok {
			return jsonTypeProfile{}, ErrTooLarge
		}
		weights[component] = weight
		mapLike = mapLike || node.mapLike
		if node.objectBytes > objectBytes {
			objectBytes = node.objectBytes
		}
		for _, edge := range node.edges {
			if !edge.cumulative {
				continue
			}
			target := componentOf[edge.target]
			if target != component {
				edges[component] = append(edges[component], target)
			}
		}
	}
	costs := make([]int64, count)
	states := make([]uint8, count)
	var cost func(int) (int64, error)
	cost = func(component int) (int64, error) {
		switch states[component] {
		case 1:
			return 0, ErrInvalid
		case 2:
			return costs[component], nil
		}
		states[component] = 1
		maximum := int64(0)
		for _, target := range edges[component] {
			candidate, err := cost(target)
			if err != nil {
				return 0, err
			}
			if candidate > maximum {
				maximum = candidate
			}
		}
		total, ok := addTransientBytes(weights[component], maximum)
		if !ok {
			return 0, ErrTooLarge
		}
		states[component] = 2
		costs[component] = total
		return total, nil
	}
	maximum := int64(0)
	for component := range count {
		candidate, err := cost(component)
		if err != nil {
			return jsonTypeProfile{}, err
		}
		if candidate > maximum {
			maximum = candidate
		}
	}
	return jsonTypeProfile{maximum: maximum, mapLike: mapLike, objectBytes: objectBytes, encodeWork: encodeWork}, nil
}

func (this *jsonTypeProfiler) cumulativeComponents() ([]int, int) {
	orders := make([]int, len(this.nodes))
	low := make([]int, len(this.nodes))
	onStack := make([]bool, len(this.nodes))
	componentOf := make([]int, len(this.nodes))
	for index := range orders {
		orders[index] = -1
		componentOf[index] = -1
	}
	stack := make([]int, 0, len(this.nodes))
	nextOrder := 0
	components := 0
	var connect func(int)
	connect = func(index int) {
		orders[index] = nextOrder
		low[index] = nextOrder
		nextOrder++
		stack = append(stack, index)
		onStack[index] = true
		for _, edge := range this.nodes[index].edges {
			if !edge.cumulative {
				continue
			}
			if orders[edge.target] < 0 {
				connect(edge.target)
				if low[edge.target] < low[index] {
					low[index] = low[edge.target]
				}
			} else if onStack[edge.target] && orders[edge.target] < low[index] {
				low[index] = orders[edge.target]
			}
		}
		if low[index] != orders[index] {
			return
		}
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			componentOf[member] = components
			if member == index {
				break
			}
		}
		components++
	}
	for index := range this.nodes {
		if orders[index] < 0 {
			connect(index)
		}
	}
	return componentOf, components
}

func jsonUnsafeHook(value reflect.Type) bool {
	jsonDecoder := reflect.TypeFor[json.Unmarshaler]()
	textDecoder := reflect.TypeFor[encoding.TextUnmarshaler]()
	jsonEncoder := reflect.TypeFor[json.Marshaler]()
	textEncoder := reflect.TypeFor[encoding.TextMarshaler]()
	if value.Implements(jsonDecoder) || value.Implements(textDecoder) || value.Implements(jsonEncoder) || value.Implements(textEncoder) {
		return true
	}
	return value.Kind() != reflect.Pointer && (reflect.PointerTo(value).Implements(jsonDecoder) || reflect.PointerTo(value).Implements(textDecoder) ||
		reflect.PointerTo(value).Implements(jsonEncoder) || reflect.PointerTo(value).Implements(textEncoder))
}

func jsonIsZeroHook(value reflect.Type) bool {
	isZero := reflect.TypeFor[interface{ IsZero() bool }]()
	if value.Implements(isZero) {
		return true
	}
	return value.Kind() != reflect.Pointer && reflect.PointerTo(value).Implements(isZero)
}

func validateSafeJSONType(value reflect.Type) error {
	switch value.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String, reflect.Array, reflect.Slice, reflect.Pointer, reflect.Struct:
		return nil
	case reflect.Map:
		switch value.Key().Kind() {
		case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return nil
		default:
			return fmt.Errorf("%w: unsupported safe JSON map key", ErrInvalid)
		}
	case reflect.Interface:
		return fmt.Errorf("%w: interfaces require TrustedJSON", ErrInvalid)
	default:
		return fmt.Errorf("%w: unsupported safe JSON type", ErrInvalid)
	}
}

func validateJSONPayloadLimit(limit PayloadLimit) error {
	return validatePayloadLimit(limit)
}

func addTransientBytes(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

func multiplyTransientBytes(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left != 0 && right > math.MaxInt64/left {
		return 0, false
	}
	return left * right, true
}
