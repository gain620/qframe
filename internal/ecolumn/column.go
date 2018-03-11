package ecolumn

import (
	"encoding/json"
	"fmt"
	"github.com/tobgu/qframe/errors"
	"github.com/tobgu/qframe/internal/column"
	"github.com/tobgu/qframe/internal/index"
	"github.com/tobgu/qframe/internal/scolumn"
	qfstrings "github.com/tobgu/qframe/internal/strings"
	"github.com/tobgu/qframe/types"
	"reflect"
	"strings"
)

type enumVal uint8

const maxCardinality = 255
const nullValue = maxCardinality

func (v enumVal) isNull() bool {
	return v == nullValue
}

func (v enumVal) compVal() int {
	// Convenience function to be able to compare null and non null values
	// in a straight forward way. Null is considered smaller than all other values.
	if v == nullValue {
		return -1
	}

	return int(v)
}

type Column struct {
	data   []enumVal
	values []string
}

// Factory is a helper used during construction of the enum column
type Factory struct {
	s         Column
	valToEnum map[string]enumVal
	strict    bool
}

func New(data []*string, values []string) (Column, error) {
	f, err := NewFactory(values, len(data))
	if err != nil {
		return Column{}, err
	}

	for _, d := range data {
		if d != nil {
			if err := f.AppendString(*d); err != nil {
				return Column{}, err
			}
		} else {
			f.AppendNil()
		}
	}

	return f.ToColumn(), nil
}

func NewConst(val *string, count int, values []string) (Column, error) {
	f, err := NewFactory(values, count)
	if err != nil {
		return Column{}, err
	}

	eV, err := f.enumVal(val)
	if err != nil {
		return Column{}, err
	}

	for i := 0; i < count; i++ {
		f.AppendEnum(eV)
	}

	return f.ToColumn(), nil
}

func NewFactory(values []string, sizeHint int) (*Factory, error) {
	if len(values) > maxCardinality {
		return nil, errors.New("New enum", "too many unique values, max cardinality is %d", maxCardinality)
	}

	if values == nil {
		values = make([]string, 0)
	}

	valToEnum := make(map[string]enumVal, len(values))
	for i, v := range values {
		valToEnum[v] = enumVal(i)
	}

	return &Factory{s: Column{
		data: make([]enumVal, 0, sizeHint), values: values},
		valToEnum: valToEnum,
		strict:    len(values) > 0}, nil
}

func (f *Factory) AppendNil() {
	f.AppendEnum(nullValue)
}

func (f *Factory) AppendEnum(val enumVal) {
	f.s.data = append(f.s.data, val)
}

func (f *Factory) AppendByteString(str []byte) error {
	if e, ok := f.valToEnum[string(str)]; ok {
		f.AppendEnum(e)
		return nil
	}

	v := string(str)
	return f.appendString(v)
}

func (f *Factory) AppendString(str string) error {
	if e, ok := f.valToEnum[str]; ok {
		f.s.data = append(f.s.data, e)
		return nil
	}

	return f.appendString(str)
}

func (f *Factory) newEnumVal(s string) enumVal {
	ev := enumVal(len(f.s.values))
	f.s.values = append(f.s.values, s)
	f.valToEnum[s] = ev
	return ev
}

func (f *Factory) enumVal(s *string) (enumVal, error) {
	if s == nil {
		return nullValue, nil
	}

	if e, ok := f.valToEnum[*s]; ok {
		return e, nil
	}

	if f.strict {
		return 0, errors.New("enum val", `unknown enum value "%s" using strict enum`, *s)
	}

	if len(f.s.values) >= maxCardinality {
		return 0, errors.New("enum val", `enum max cardinality (%d) exceeded`, maxCardinality)
	}

	return f.newEnumVal(*s), nil
}

func (f *Factory) appendString(str string) error {
	if f.strict {
		return errors.New("append enum val", `unknown enum value "%s" using strict enum`, str)
	}

	if len(f.s.values) >= maxCardinality {
		return errors.New("append enum val", `enum max cardinality (%d) exceeded`, maxCardinality)
	}

	ev := f.newEnumVal(str)
	f.s.data = append(f.s.data, ev)
	return nil
}

func (f *Factory) ToColumn() Column {
	// Using the factory after this method has been called and the column exposed
	// is not recommended.
	return f.s
}

var enumApplyFuncs = map[string]func(index.Int, Column) (interface{}, error){
	"ToUpper": toUpper,
}

func toUpper(_ index.Int, s Column) (interface{}, error) {
	// This demonstrates how built in functions can be made a lot more
	// efficient than the current general functions.
	// In this example the upper function only has to be applied once to
	// every enum value instead of once to every element. The data field
	// can be kept as is.
	newValues := make([]string, len(s.values))
	for i, s := range s.values {
		newValues[i] = strings.ToUpper(s)
	}

	return Column{data: s.data, values: newValues}, nil
}

func (c Column) Len() int {
	return len(c.data)
}

func (c Column) StringAt(i uint32, naRep string) string {
	v := c.data[i]
	if v.isNull() {
		return naRep
	}

	return c.values[v]
}

func (c Column) AppendByteStringAt(buf []byte, i uint32) []byte {
	enum := c.data[i]
	if enum.isNull() {
		return append(buf, "null"...)
	}

	return qfstrings.AppendQuotedString(buf, c.values[enum])
}

type marshaler struct {
	Column
	index index.Int
}

func (m marshaler) MarshalJSON() ([]byte, error) {
	buf := make([]byte, 0, len(m.index))
	buf = append(buf, '[')
	for i, ix := range m.index {
		if i > 0 {
			buf = append(buf, ',')
		}

		enum := m.data[ix]
		if enum.isNull() {
			buf = append(buf, "null"...)
		} else {
			buf = qfstrings.AppendQuotedString(buf, m.values[enum])
		}
	}

	buf = append(buf, ']')
	return buf, nil
}

func (c Column) Marshaler(index index.Int) json.Marshaler {
	return marshaler{Column: c, index: index}
}

func (c Column) ByteSize() int {
	totalSize := 2 * 2 * 8 // Slice headers
	for _, s := range c.values {
		totalSize += len(s)
	}
	totalSize += len(c.data)
	return totalSize
}

func (c Column) Equals(index index.Int, other column.Column, otherIndex index.Int) bool {
	otherE, ok := other.(Column)
	if !ok {
		return false
	}

	for ix, x := range index {
		enumVal := c.data[x]
		oEnumVal := otherE.data[otherIndex[ix]]
		if enumVal.isNull() || oEnumVal.isNull() {
			if enumVal == oEnumVal {
				continue
			}
			return false
		}

		if c.values[enumVal] != otherE.values[oEnumVal] {
			return false
		}
	}

	return true
}

func (c Comparable) Compare(i, j uint32) column.CompareResult {
	x, y := c.s.data[i], c.s.data[j]
	if x.isNull() || y.isNull() {
		if !x.isNull() {
			return c.gtValue
		}

		if !y.isNull() {
			return c.ltValue
		}

		// Consider nil == nil, this means that we can group
		// by null values for example (this differs from Pandas)
		return column.Equal
	}

	if x < y {
		return c.ltValue
	}

	if x > y {
		return c.gtValue
	}

	return column.Equal
}

func equalTypes(s1, s2 Column) bool {
	if len(s1.values) != len(s2.values) || len(s1.data) != len(s2.data) {
		return false
	}

	for i, val := range s1.values {
		if val != s2.values[i] {
			return false
		}
	}

	return true
}

func (c Column) filterWithBitset(index index.Int, bset *bitset, bIndex index.Bool) {
	for i, x := range bIndex {
		if !x {
			enum := c.data[index[i]]
			bIndex[i] = bset.isSet(enum)
		}
	}
}

func (c Column) filterBuiltIn(index index.Int, comparator string, comparatee interface{}, bIndex index.Bool) error {
	switch comp := comparatee.(type) {
	case string:
		if compFunc, ok := filterFuncs[comparator]; ok {
			for i, value := range c.values {
				if value == comp {
					compFunc(index, c.data, enumVal(i), bIndex)
					return nil
				}
			}

			return errors.New("Filter enum", "Unknown enum value in filter argument: %c", comp)
		}

		if multiFunc, ok := multiFilterFuncs[comparator]; ok {
			bset, err := multiFunc(comp, c.values)
			if err != nil {
				return errors.Propagate("Filter enum", err)
			}

			c.filterWithBitset(index, bset, bIndex)
			return nil
		}

		return errors.New("Filter enum", "unknown comparison operator, %v", comparator)
	case []string:
		if multiFunc, ok := multiInputFilterFuncs[comparator]; ok {
			bset := multiFunc(qfstrings.NewStringSet(comp), c.values)
			c.filterWithBitset(index, bset, bIndex)
			return nil
		}

		return errors.New("Filter enum", "unknown comparison operator, %v", comparator)
	case Column:
		if ok := equalTypes(c, comp); !ok {
			return errors.New("Filter enum", "cannot compare enums of different types")
		}

		compFunc, ok := filterFuncs2[comparator]
		if !ok {
			return errors.New("Filter enum", "unknown comparison operator, %v", comparator)
		}

		compFunc(index, c.data, comp.data, bIndex)
		return nil
	default:
		return errors.New("Filter enum", "invalid comparison type, %c, expected string or other enum column", reflect.TypeOf(comparatee))
	}
}

func (c Column) filterCustom1(index index.Int, fn func(*string) bool, bIndex index.Bool) {
	for i, x := range bIndex {
		if !x {
			bIndex[i] = fn(c.stringPtrAt(index[i]))
		}
	}
}

func (c Column) filterCustom2(index index.Int, fn func(*string, *string) bool, comparatee interface{}, bIndex index.Bool) error {
	otherC, ok := comparatee.(Column)
	if !ok {
		return errors.New("filter string", "expected comparatee to be string column, was %v", reflect.TypeOf(comparatee))
	}

	for i, x := range bIndex {
		if !x {
			bIndex[i] = fn(c.stringPtrAt(index[i]), otherC.stringPtrAt(index[i]))
		}
	}

	return nil
}

func (c Column) Filter(index index.Int, comparator interface{}, comparatee interface{}, bIndex index.Bool) error {
	var err error
	switch t := comparator.(type) {
	case string:
		err = c.filterBuiltIn(index, t, comparatee, bIndex)
	case func(*string) bool:
		c.filterCustom1(index, t, bIndex)
	case func(*string, *string) bool:
		err = c.filterCustom2(index, t, comparatee, bIndex)
	default:
		err = errors.New("filter string", "invalid filter type %v", reflect.TypeOf(comparator))
	}
	return err
}

func (c Column) subset(index index.Int) Column {
	data := make([]enumVal, 0, len(index))
	for _, ix := range index {
		data = append(data, c.data[ix])
	}

	return Column{data: data, values: c.values}
}

func (c Column) Subset(index index.Int) column.Column {
	return c.subset(index)
}

func (c Column) stringSlice(index index.Int) []*string {
	result := make([]*string, 0, len(index))
	for _, ix := range index {
		v := c.data[ix]
		if v.isNull() {
			result = append(result, nil)
		} else {
			result = append(result, &c.values[v])
		}
	}
	return result
}

func (c Column) Comparable(reverse bool) column.Comparable {
	if reverse {
		return Comparable{s: c, ltValue: column.GreaterThan, gtValue: column.LessThan}
	}

	return Comparable{s: c, ltValue: column.LessThan, gtValue: column.GreaterThan}
}

func (c Column) String() string {
	strs := make([]string, len(c.data))
	for i, v := range c.data {
		if v.isNull() {
			// For now
			strs[i] = "null"
		} else {
			strs[i] = c.values[v]
		}
	}

	return fmt.Sprintf("%v", strs)
}

func (c Column) Aggregate(indices []index.Int, fn interface{}) (column.Column, error) {
	// NB! The result of aggregating over an enum column is a string column
	switch t := fn.(type) {
	case string:
		// There are currently no build in aggregations for enums
		return nil, errors.New("enum aggregate", "aggregation function %c is not defined for enum column", fn)
	case func([]*string) *string:
		data := make([]*string, 0, len(indices))
		for _, ix := range indices {
			data = append(data, t(c.stringSlice(ix)))
		}
		return scolumn.New(data), nil
	default:
		return nil, errors.New("enum aggregate", "invalid aggregation function type: %v", t)
	}
}

func (c Column) stringPtrAt(i uint32) *string {
	if c.data[i].isNull() {
		return nil
	}
	return &c.values[c.data[i]]
}

func (c Column) Apply1(fn interface{}, ix index.Int) (interface{}, error) {
	/*
		Interesting optimisations could be applied here given that:
		- The passed in function always returns the same value given the same input
		- Or, for enums a given restriction is that the functions will only be called once for each value
		In that case a mapping between the enum value and the result could be set up to avoid having to
		call the function multiple times for the same input.
	*/
	var err error
	switch t := fn.(type) {
	case func(*string) (int, error):
		result := make([]int, len(c.data))
		for _, i := range ix {
			if result[i], err = t(c.stringPtrAt(i)); err != nil {
				return nil, err
			}
		}
		return result, nil
	case func(*string) (float64, error):
		result := make([]float64, len(c.data))
		for _, i := range ix {
			if result[i], err = t(c.stringPtrAt(i)); err != nil {
				return nil, err
			}
		}
		return result, nil
	case func(*string) (bool, error):
		result := make([]bool, len(c.data))
		for _, i := range ix {
			if result[i], err = t(c.stringPtrAt(i)); err != nil {
				return nil, err
			}
		}
		return result, nil
	case func(*string) (*string, error):
		result := make([]*string, len(c.data))
		for _, i := range ix {
			if result[i], err = t(c.stringPtrAt(i)); err != nil {
				return nil, err
			}
		}
		return result, nil
	case string:
		if f, ok := enumApplyFuncs[t]; ok {
			return f(ix, c)
		}
		return nil, errors.New("string.apply1", "unknown built in function %c", t)
	default:
		return nil, errors.New("enum.apply1", "cannot apply type %#v to column", fn)
	}
}

func (c Column) Apply2(fn interface{}, s2 column.Column, ix index.Int) (column.Column, error) {
	s2S, ok := s2.(Column)
	if !ok {
		return nil, errors.New("enum.apply2", "invalid column type %v", reflect.TypeOf(s2))
	}

	switch t := fn.(type) {
	case func(*string, *string) (*string, error):
		var err error
		result := make([]*string, len(c.data))
		for _, i := range ix {
			if result[i], err = t(c.stringPtrAt(i), s2S.stringPtrAt(i)); err != nil {
				return nil, err
			}
		}

		// NB! String column returned here, not enum. Returning enum could result
		// in unforeseen results (eg. it would not always fit in an enum, the order
		// is not given).
		return scolumn.New(result), nil
	case string:
		// No built in functions for strings at this stage
		return nil, errors.New("enum.apply2", "unknown built in function %c", t)
	default:
		return nil, errors.New("enum.apply2", "cannot apply type %#v to column", fn)
	}
}

func (c Column) View(ix index.Int) View {
	return View{column: c, index: ix}
}

func (c Column) FunctionType() types.FunctionType {
	return types.FunctionTypeString
}

type Comparable struct {
	s       Column
	ltValue column.CompareResult
	gtValue column.CompareResult
}
