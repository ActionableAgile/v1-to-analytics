package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

const PRINT_JSON = true

// VersionOne data structures for unmarshalling from JSON
type VOneAssetList struct {
	Total  int
	Assets []VOneAsset
}

type VOneAsset struct {
	Attributes VOneAttributes
	Id         string // asset ID for link
}

type VOneAttributes struct {
	Name       VOneAttribute
	Number     VOneAttribute
	ChangeDate VOneAttribute
	Status     VOneAttribute      `json:"Status.Name"`
	Scope      VOneAttribute      `json:"Scope.Name"`
	Timebox    VOneAttribute      `json:"Timebox.Name"`
	Theme      VOneArrayAttribute `json:"Parent.Now.ParentMeAndUp.Name"`
}

type VOneAttribute struct {
	Value string
}

type VOneArrayAttribute struct {
	Value []string
}

// collect work items for writing to file
type Item struct {
	Id         string
	Link       string
	Name       string
	StageDates []string
	Attributes []string   // values only; keys are from Config.attributes
	Events     [][]string // workspace for stage date algorithm
}

type ClauseMaker func(param string) (clause string)

func getQuery(index, batchSize int, config *Config) (escapedURL, unescapedURL string) {

	// collect the "select" parts of the query
	selParts := []string{"Name", "Number", "Status.Name", "ChangeDate", "Parent.Now.ParentMeAndUp"}
	for _, a := range config.Attributes {
		switch a.FieldName {
		case "Scope":
			selParts = append(selParts, "Scope.Name")
		case "Timebox":
			selParts = append(selParts, "Timebox.Name")
		}
	}

	// collect the "where" parts of the query like this (A | B) & (Red | Blue) & (Cheese | Wine)
	var whereParts []string
	whereParts = addWherePart(config.ScopeNames,
		func(p string) string { return "Scope.Name='" + p + "'" }, whereParts)
	whereParts = addWherePart(config.TimeboxNames,
		func(p string) string { return "Timebox.Name='" + p + "'" }, whereParts)
	whereParts = addWherePart(config.Themes,
		func(p string) string { return "Parent.Now.ParentMeAndUp.Name='" + p + "'" }, whereParts)
	whereString := strings.Join(whereParts, ";") // logical AND
	if len(whereParts) > 1 {
		whereString = "(" + whereString + ")"
	}

	// build url
	unescapedURL = config.Domain + "/rest-1.v1/Hist/Story?sel=" + strings.Join(selParts, ",")
	escapedURL = unescapedURL // nothing to escape yet
	if len(whereString) > 0 {
		unescapedURL += "&where=" + whereString
		escapedURL += "&where=" + url.QueryEscape(whereString)
	}
	page := fmt.Sprintf("&page=%v,%v", batchSize, index)
	unescapedURL += page
	escapedURL += page
	return
}

// Extract batchSize assets starting at index and use them to build items.
// Multiple assets are required to make an item, so we have to align the batch and
//   discard extra assets in each batch.
func getItems(index, batchSize int, config *Config) (items []*Item, used, left int, err error) {

	escapedURL, _ := getQuery(index, batchSize, config)

	// get credentials
	credentials, err := config.GetCredentials()
	if err != nil {
		return nil, 0, 0, err
	}

	// send the request
	client := http.Client{}
	req, _ := http.NewRequest("GET", escapedURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+credentials)
	resp, _ := client.Do(req)

	// process the response
	if resp != nil && resp.StatusCode == 200 { // OK
		defer resp.Body.Close()

		// print the json
		if PRINT_JSON {
			bodyBytes, _ := ioutil.ReadAll(resp.Body)
			bodyString := string(bodyBytes)
			fmt.Println(bodyString)
			return items, 0, 0, nil
		}

		// decode json
		var list VOneAssetList
		json.NewDecoder(resp.Body).Decode(&list)

		// extract work item info
		item := NewItem(list.Assets[0].Attributes.Number.Value, config)
		for i, asset := range list.Assets {

			// handle new item
			id := asset.Attributes.Number.Value
			if id != item.Id {

				// finish up the old one
				item.ApplyEvents(config)
				items = append(items, item)
				used = i

				// start a new one
				item = NewItem(id, config)
			}

			// extract link id X from "Story:X,Y"
			fields := strings.Split(asset.Id, ":")
			if len(fields) == 3 {
				item.Link = fields[1]
			}

			// extract name, which must not contain characters that cause problems for CSV
			item.Name = cleanString(asset.Attributes.Name.Value)

			// build stage dates by accumulating out-of-order events to handle backward flow
			stageString := asset.Attributes.Status.Value
			if len(stageString) == 0 {
				stageString = "(None)"
			}
			if stageIndex, found := config.StageMap[stageString]; found {
				date := strings.SplitN(asset.Attributes.ChangeDate.Value, "T", 2)[0]
				item.Events[stageIndex] = append(item.Events[stageIndex], date)
			} else if len(stageString) > 0 {
				//fmt.Println("Unused stage", stageString)
			}

			// extract attributes
			for i, a := range config.Attributes {
				switch a.FieldName {
				case "Scope":
					item.Attributes[i] = asset.Attributes.Scope.Value
				case "Timebox":
					item.Attributes[i] = asset.Attributes.Timebox.Value
				case "Theme":
					values := asset.Attributes.Theme.Value
					if len(values) > 0 {
						lastValue := values[len(values)-1]
						item.Attributes[i] = lastValue
					}
				}
			}

			// handle last item of last batch
			if index+i+1 == list.Total {
				item.ApplyEvents(config)
				items = append(items, item)
				used = i + 1
			}
		}

		left = list.Total - index - used
	} else {
		// it doesn't matter why because since all the ids worked we're just going to retry
		return nil, 0, 0, fmt.Errorf("Failed")
	}

	return items, used, left, nil
}

func addWherePart(pieces []string, cm ClauseMaker, whereParts []string) []string {
	if len(pieces) > 0 {
		var subParts []string
		for _, piece := range pieces {
			subParts = append(subParts, cm(piece))
		}
		s := strings.Join(subParts, "|")
		if len(subParts) > 1 {
			s = "(" + s + ")"
		}
		return append(whereParts, s)
	}
	return whereParts
}

func cleanString(s string) string {
	s = strings.Replace(s, "\"", "", -1) // remove quotes
	s = strings.Replace(s, ",", "", -1)  // remove commas
	s = strings.Replace(s, "\\", "", -1) // remove backslashes
	return "\"" + s + "\""
}

// put quotes around it unless it already has them
func quoteString(s string) string {
	if strings.HasPrefix(s, "\"") {
		return s
	}
	return "\"" + s + "\""
}

func NewItem(key string, config *Config) *Item {
	return &Item{
		Id:         key,
		StageDates: make([]string, len(config.StageNames)),
		Attributes: make([]string, len(config.Attributes)),
		Events:     make([][]string, len(config.StageNames)),
	}
}

// for each stage use min date that is >= max date from previous stages
func (item *Item) ApplyEvents(config *Config) {
	previousMaxDate := ""
	for stageIndex := range config.StageNames {
		stageBestDate := ""
		stageMaxDate := ""
		for _, date := range item.Events[stageIndex] {
			if date >= previousMaxDate && (stageBestDate == "" || date < stageBestDate) {
				stageBestDate = date
			}
			if date > stageMaxDate {
				stageMaxDate = date
			}
		}
		if stageBestDate != "" {
			item.StageDates[stageIndex] = stageBestDate
		}
		if stageMaxDate != "" && stageMaxDate > previousMaxDate {
			previousMaxDate = stageMaxDate
		}
	}
}

func (item *Item) HasDate() bool {
	result := false
	for _, date := range item.StageDates {
		if len(date) > 0 {
			result = true
			break
		}
	}
	return result
}

func (item *Item) toCSV(config *Config) string {
	var buffer bytes.Buffer
	buffer.WriteString(item.Id)
	buffer.WriteString("," + item.Link)
	buffer.WriteString("," + item.Name)
	for _, stageDate := range item.StageDates {
		buffer.WriteString("," + stageDate)
	}
	for _, value := range item.Attributes {
		buffer.WriteString("," + value)
	}
	return buffer.String()
}

func (item *Item) toJSON(config *Config) string {
	var data []string
	data = append(data, strings.TrimSpace(item.Id))
	data = append(data, config.Domain+"/browse/"+item.Id)
	data = append(data, strings.TrimSpace(item.Name))
	for _, stageDate := range item.StageDates {
		data = append(data, stageDate)
	}
	for _, value := range item.Attributes {
		data = append(data, strings.TrimSpace(value))
	}
	result, _ := json.Marshal(data)
	return string(result)
}
