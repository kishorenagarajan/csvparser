package csvparser

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// ParserFunc is the callback that will be called at each column parsing/reading
// The value parameter is the column value, and the destination is the struct to add values from the parsing
type ParserFunc[ReadTo any] func(value string, destination *ReadTo) error

// AfterParsingRowFunc is a callback/hook that will run after each row is parsed.
type AfterParsingRowFunc[ReadTo any] func(parsedObject ReadTo)

// OnErrorFunc is a callback that will run after every parsing error.
type OnErrorFunc func(row []string, err error)

// CsvParser is the internal object that will keep all the references needed to parse the file
type CsvParser[ReadTo any] struct {
	fileReader              *csv.Reader
	columnParsers           map[string]ParserFunc[ReadTo]
	onError                 OnErrorFunc
	afterParsingHook        AfterParsingRowFunc[ReadTo]
	headers                 []string
	onFinish                func()
	onStart                 func()
	terminateOnParsingError bool
}

// NewCsvParserFromBytes instantiates a new CsvParser from a []byte input
// The *headers parameter are necessary if your .csv file doesn't contain headers
// by default. Adding headers to the constructor will make the parser know what to handle.
func NewCsvParserFromBytes[ReadTo any](input []byte, headers ...string) *CsvParser[ReadTo] {
	reader := bytes.NewReader(input)
	return NewCsvParserFromReader[ReadTo](reader, headers...)
}

// NewCsvParserFromReader instantiates a new CsvParser from an io.Reader directly.
// Useful when reading from multipart files.
// The *headers parameter are necessary if your .csv file doesn't contain headers
// by default. Adding headers to the constructor will make the parser know what to handle.
func NewCsvParserFromReader[ReadTo any](input io.Reader, headers ...string) *CsvParser[ReadTo] {
	return &CsvParser[ReadTo]{
		fileReader:    csv.NewReader(input),
		headers:       headers,
		columnParsers: map[string]ParserFunc[ReadTo]{},
	}
}

// TerminateOnParsingError sets a flag to finish the parsing if a single row throws an error.
// if flag is set to false, it will continue to parse and skip the record with the error.
func (c *CsvParser[ReadTo]) TerminateOnParsingError() *CsvParser[ReadTo] {
	c.terminateOnParsingError = true
	return c
}

// OnParseError sets a callback that is supposed to be run after a row has a parsing error
func (c *CsvParser[ReadTo]) OnParseError(callback OnErrorFunc) *CsvParser[ReadTo] {
	c.onError = callback
	return c
}

// AfterEachParsingHook adds a handler that will run after every single parsing
func (c *CsvParser[ReadTo]) AfterEachParsingHook(handler AfterParsingRowFunc[ReadTo]) *CsvParser[ReadTo] {
	c.afterParsingHook = handler
	return c
}

// OnFinish adds a handler that will run at the end of the file parsing.
func (c *CsvParser[ReadTo]) OnFinish(handler func()) *CsvParser[ReadTo] {
	c.onFinish = handler
	return c
}

// OnStart adds a handler that will run at the start of the file parsing.
func (c *CsvParser[ReadTo]) OnStart(handler func()) *CsvParser[ReadTo] {
	c.onStart = handler
	return c
}

// AddColumnParser adds a parser for each column to the internal parser list
func (c *CsvParser[ReadTo]) AddColumnParser(headerName string, parser ParserFunc[ReadTo]) *CsvParser[ReadTo] {
	c.columnParsers[headerName] = parser
	return c
}

// Parse returns an array of the object to return ([]ReadTo) from the input data and parsers provided.
func (c *CsvParser[ReadTo]) Parse() ([]ReadTo, error) {
	c.runOnStart()
	err := c.prepareHeaders()
	if err != nil {
		return []ReadTo{}, err
	}
	res, err := c.parseResults()
	c.runOnFinish()
	return res, err
}

// prepareHeaders verifies if the headers and parsers are matched. If the headers are not passed in the constructor,
// it will load the headers from the file data.
func (c *CsvParser[ReadTo]) prepareHeaders() error {
	if c.areHeadersEmpty() {
		return c.loadHeadersFromFile()
	}
	header, existsUnparsableHeader := c.isThereAnUnparsableHeader()
	if existsUnparsableHeader {
		return newUnparsableHeaderErr(header)
	}
	return nil
}

// areHeadersEmpty checks if the headers are empty
func (c *CsvParser[ReadTo]) areHeadersEmpty() bool {
	return len(c.headers) == 0
}

// areHeadersAndParsersMatched makes sure that each header has an equivalent parser.
func (c *CsvParser[ReadTo]) isThereAnUnparsableHeader() (string, bool) {
	for _, header := range c.headers {
		if !c.existsParserForHeader(header) {
			return header, true
		}
	}
	return "", false
}

// existsParserForHeader verifies if there is a parser defined for a specific header
func (c *CsvParser[ReadTo]) existsParserForHeader(header string) bool {
	_, ok := c.getParserFor(header)
	return ok
}

// loadHeadersFromFile reads the first row in the file and loads it into the headers
func (c *CsvParser[ReadTo]) loadHeadersFromFile() error {
	headers, err := c.fileReader.Read()
	if err != nil {
		return parseError{Msg: fmt.Sprintf("couldn't read headers from file: %s", err.Error())}
	}
	return c.loadHeaders(headers)
}

// loadHeaders loads a set of headers into the struct.
func (c *CsvParser[ReadTo]) loadHeaders(headers []string) error {
	for _, header := range headers {
		if err := c.loadHeader(header); err != nil {
			return err
		}
	}
	return nil
}

// loadHeader loads one header into the struct. If it is not able to be parsed
// (doesn't have an associated parser), it will return an error.
func (c *CsvParser[ReadTo]) loadHeader(header string) error {
	header = strings.Trim(header, " ")
	if !c.isHeaderAbleToBeParsed(header) {
		return newUnparsableHeaderErr(header)
	}
	c.headers = append(c.headers, header)
	return nil
}

// isHeaderAbleToBeParsed verifies if there is a correspondent parser for said header.
func (c *CsvParser[ReadTo]) isHeaderAbleToBeParsed(header string) bool {
	_, ok := c.getParserFor(header)
	return ok
}

// getParserFor gets a parser for a specific header.
func (c *CsvParser[ReadTo]) getParserFor(header string) (ParserFunc[ReadTo], bool) {
	res, ok := c.columnParsers[header]
	return res, ok
}

// parseResults returns the slice of objects to be parsed from the .csv file.
func (c *CsvParser[ReadTo]) parseResults() ([]ReadTo, error) {
	result := make([]ReadTo, 0)
	for {
		object, err := c.readRowAndParseObject()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !c.terminateOnParsingError {
				continue
			}
			return []ReadTo{}, newparseError(err)
		}
		result = append(result, *object)
	}
	return result, nil
}

// readRowAndParseObject reads a file row and parses it into an object.
func (c *CsvParser[ReadTo]) readRowAndParseObject() (*ReadTo, error) {
	row, err := c.fileReader.Read()
	if err != nil {
		return nil, err
	}
	return c.parseRow(row)
}

// parseRow parses a single row into the target object. Runs the hook for the object if success.
func (c *CsvParser[ReadTo]) parseRow(row []string) (*ReadTo, error) {
	object := new(ReadTo)
	err := c.parseColumns(row, object)
	if err != nil {
		c.runOnError(row, err)
		return nil, err
	}
	c.runAfterParsingHook(object)
	return object, nil
}

// runOnError runs the onError callback.
func (c *CsvParser[ReadTo]) runOnError(row []string, err error) {
	if c.onErrorExists() {
		c.onError(row, err)
	}
}

func (c *CsvParser[ReadTo]) onErrorExists() bool {
	return c.onError != nil
}

// runHook runs the hook that is set up in the struct
func (c *CsvParser[ReadTo]) runAfterParsingHook(object *ReadTo) {
	if c.afterParsingHookExists() {
		c.afterParsingHook(*object)
	}
}

func (c *CsvParser[ReadTo]) afterParsingHookExists() bool {
	return c.afterParsingHook != nil
}

// parseColumns parses all the columns into a destination object.
func (c *CsvParser[ReadTo]) parseColumns(row []string, destination *ReadTo) error {
	for i, columnValue := range row {
		err := c.parseColumn(columnValue, c.headers[i], destination)
		if err != nil {
			return err
		}
	}
	return nil
}

// parseColumn parses a single column. Uses columnParsers from the columnHeader to do it.
func (c *CsvParser[ReadTo]) parseColumn(columnValue, columnHeader string, destination *ReadTo) error {
	parser, ok := c.getParserFor(columnHeader)
	if !ok {
		return newUnparsableHeaderErr(columnHeader)
	}
	if err := parser(columnValue, destination); err != nil {
		return err
	}
	return nil
}

func (c *CsvParser[ReadTo]) runOnStart() {
	if c.onStart != nil {
		c.onStart()
	}
}

func (c *CsvParser[ReadTo]) runOnFinish() {
	if c.onFinish != nil {
		c.onFinish()
	}
}

/*
Given array of structs from the parsed csv, prints out the contents of the file parsing in a nice string format.
The input of the function can take in any type of structs with any number of fields within the struct.
*/
func ParseToString(arr []interface{}) string {
	result := ""

	// For each struct in the given array
	for j, val := range arr {
		line := "|-> ("

		// Use reflect package to obtain number of fields in struct
		v := reflect.ValueOf(val)
		values := make([]interface{}, v.NumField())

		// Store array of each field in the struct
		for i := 0; i < v.NumField(); i++ {
			values[i] = v.Field(i).Interface()
		}

		if j == 0 {
			// Add top outline of the printed result
			for k := 0; k < 20*(v.NumField())+10; k++ {
				result += "_"
			}
			result += "\n"
		}

		for i, printVal := range values {
			// Translate each field to a string. Each field started as an interface.
			str := fmt.Sprintf("%v", printVal)
			line += str

			if i < len(values)-1 {
				// Add buffer of spaces between each field on every row. The buffer size is set to 25 chars.
				for i := 0; i < 25-len(str); i++ {
					line += " "
				}
			} else {
				line += ")"
			}

		}
		if j != 0 {
			result += "\n"
		}
		result += line
	}

	// Return string of the struct contents in a more readable format
	return result
}
