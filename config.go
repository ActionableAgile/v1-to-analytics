package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

var sections = []string{"Connection", "Criteria", "Workflow", "Attributes"}
var connectionKeys = []string{"Domain", "Username", "Password"}
var criteriaKeys = []string{"Scopes", "Timeboxes", "Themes"}
var attributeFields = []string{"Scope", "Timebox", "Theme"}

type ConfigAttr struct {
	ColumnName string // CSV column name
	FieldName  string // Jira field from attributeFields above or a customfield_...
}

type Config struct {

	// connection stuff
	Domain   string
	Username string
	Password string

	// criteria stuff
	ScopeNames   []string // project
	TimeboxNames []string // sprint
	Themes       []string // types

	// workflow stuff
	StageNames         []string
	StageMap           map[string]int
	CreateInFirstStage bool

	// attributes stuff
	Attributes []ConfigAttr
}

func (c *Config) GetCredentials() (credentials string, err error) {
	if len(c.Password) > 0 {
		credentials = base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))
	} else {
		err = fmt.Errorf("Missing password")
	}
	return
}

func LoadConfigFromLines(lines []string) (*Config, error) {
	config := Config{StageMap: make(map[string]int), CreateInFirstStage: false}

	// parse the contents
	properties := make(map[string]string) // for all predefined keys (in Connection or Optional)
	section := ""
	for i, line := range lines {
		if strings.HasPrefix(line, "---") { // skip yaml indicator
		} else if strings.HasPrefix(line, "#") { // skip comments
		} else if len(strings.TrimSpace(line)) == 0 { // skip blank lines
		} else {
			parts := strings.SplitN(line, ":", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if len(key) > 0 {
				if line[0] != ' ' && line[0] != '\t' { // not indented, so start a new section
					if in(key, sections) {
						section = key
					} else {
						return nil, fmt.Errorf("Unexpected section %v at line %v", key, i+1)
					}
				} else { // indented, so handle section content
					switch section {
					case "Connection":
						if in(key, connectionKeys) {
							properties[key] = value
						} else {
							return nil, fmt.Errorf("Unexpected property %v at line %v", key, i+1)
						}
					case "Criteria":
						if in(key, criteriaKeys) {
							properties[key] = value
						} else {
							return nil, fmt.Errorf("Unexpected property %v at line %v", key, i+1)
						}
					case "Workflow":
						stageIndex := len(config.StageNames)
						config.StageNames = append(config.StageNames, key)
						for _, value := range strings.Split(value, ",") {
							value = strings.TrimSpace(value)
							if strings.ToLower(value) == "(created)" {
								if stageIndex == 0 {
									config.CreateInFirstStage = true
								} else {
									return nil, fmt.Errorf("(Created) cannot be used in non-first"+
										" stage %v at line %v", config.StageNames[stageIndex], i+1)
								}
							} else {
								config.StageMap[value] = stageIndex
							}
						}
					case "Attributes":
						if in(value, attributeFields) {
							config.Attributes = append(config.Attributes, ConfigAttr{key, value})
						} else {
							return nil, fmt.Errorf("Unknown attribute %v at line %v", value, i+1)
						}
					default:
						return nil, fmt.Errorf("Can't parse config file at line %v (extra indent?)",
							i+1)
					}
				}
			}
		}
	}

	// extract required domain and build url root
	config.Domain = properties["Domain"]
	if len(config.Domain) == 0 {
		return nil, fmt.Errorf("Config file has no property \"Domain\"")
	}

	// extract required username and optional password
	config.Username = properties["Username"]
	if len(config.Username) == 0 {
		return nil, fmt.Errorf("Config file has no property \"Username\"")
	}
	config.Password = properties["Password"]

	// collect scope (project) names
	if s, ok := properties["Scopes"]; ok {
		config.ScopeNames = parseList(s)
	}

	// collect timebox (sprint) names
	if s, ok := properties["Timeboxes"]; ok {
		config.TimeboxNames = parseList(s)
	}

	// collect theme (type) names
	if s, ok := properties["Themes"]; ok {
		config.Themes = parseList(s)
	}

	return &config, nil
}

func LoadConfigFromFile(path string) (*Config, error) {

	// open the file
	file, err := os.Open(path)
	defer file.Close()
	if err != nil {
		return nil, err
	}

	// read in the lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}

	return LoadConfigFromLines(lines)
}

// extract items from string and single-quote them
func parseList(commaDelimited string) []string {
	result := strings.Split(commaDelimited, ",")
	for i, s := range result {
		result[i] = strings.TrimSpace(s)
	}
	return result
}

// return whether the string s is found in array a
func in(s string, a []string) bool {
	for _, as := range a {
		if as == s {
			return true
		}
	}
	return false
}
