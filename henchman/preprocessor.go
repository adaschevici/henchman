package henchman

import (
	"fmt"
	log "gopkg.in/Sirupsen/logrus.v0"
	"io/ioutil"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type PlanProxy struct {
	Name            string       `yaml:"name"`
	Sudo            bool         `yaml:"sudo"`
	TaskProxies     []*TaskProxy `yaml:"tasks"`
	VarsProxy       *VarsProxy   `yaml:"vars"`
	InventoryGroups []string     `yaml:"hosts"`
}

// Task is for the general Task format.  Refer to task.go
// Vars are kept in scope for each Task.  So there is a Vars
// field for each task
// Include is the file name for the included Tasks list
type TaskProxy struct {
	Task        `yaml:",inline"`
	SudoState   string
	Include     string
	IncludeVars VarsMap `yaml:"vars"`
}

type VarsProxy struct {
	Vars VarsMap
}

// Custom unmarshaller which account for multiple include statements and include types
// NOTE: Cannot account for double includes because unmarshal(&vMap) already does
//       under the hood unmarshaling and does what any map would do, which is override
//       repeating key values
func (vp *VarsProxy) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var vMap map[string]interface{}
	var found bool
	numInclude := 0

	err := unmarshal(&vMap)
	if err != nil {
		return HenchErr(err,
			log.Fields{
				"solution": "Check for Yaml formatting errors.  Usually indentation with tabs",
			}, "")
	}

	vp.Vars = make(VarsMap)
	for field, val := range vMap {
		switch field {
		case "include":
			vp.Vars["include"], found = val.([]interface{})
			if !found {
				return HenchErr(ErrWrongType(field, val, "[]interface{}"), log.Fields{
					"solution": "Make sure the field is of proper type",
				}, "")
			}

			numInclude++
			if numInclude > 1 {
				return HenchErr(fmt.Errorf("Can only have one include statement at Vars level."),
					log.Fields{
						"solution": "remove extra include statements",
					}, "")
			}
		default:
			vp.Vars[field] = val
		}
	}

	return nil
}

// Custom unmarshaller which accounts for module names
func (tp *TaskProxy) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var tmap map[string]interface{}
	var found bool
	numModule := 0

	err := unmarshal(&tmap)
	if err != nil {
		return HenchErr(err,
			log.Fields{
				"solution": "Check for Yaml formatting errors.  Usually indentation with tabs",
			}, "")
	}
	for field, val := range tmap {
		switch field {
		case "name":
			tp.Name, found = val.(string)
			if !found {
				return HenchErr(ErrWrongType(field, val, "string"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
		case "sudo":
			tp.Sudo, found = val.(bool)
			if !found {
				return HenchErr(ErrWrongType(field, val, "bool"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
			tp.SudoState = strconv.FormatBool(tp.Sudo)
		case "ignore_errors":
			tp.IgnoreErrors, found = val.(bool)
			if !found {
				return HenchErr(ErrWrongType(field, val, "bool"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
		case "local":
			tp.Local, found = val.(bool)
			if !found {
				return HenchErr(ErrWrongType(field, val, "bool"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
		case "when":
			tp.When, found = val.(string)
			if !found {
				return HenchErr(ErrWrongType(field, val, "string"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
			if strings.Contains(tp.When, "{{") || strings.Contains(tp.When, "}}") {
				return HenchErr(fmt.Errorf("When field should not include {{ or }}"), log.Fields{
					"task":     tp.Name,
					"solution": "Remove {{ or }} from the when field",
				}, "")
			}
		case "register":
			tp.Register, found = val.(string)
			if !found {
				return HenchErr(ErrWrongType(field, val, "string"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
			if len(strings.Fields(tp.Register)) > 1 {
				return HenchErr(ErrNotValidVariable(tp.Register), log.Fields{
					"task":     tp.Name,
					"solution": "Registers must be a single word w/o spaces",
				}, "")
			}
			if isKeyword(tp.Register) {
				return HenchErr(ErrKeyword(tp.Register), log.Fields{
					"task":     tp.Name,
					"solution": "Avoid the key words: vars, item",
				}, "")
			}
		case "include":
			tp.Include, found = val.(string)
			if !found {
				return HenchErr(ErrWrongType(field, val, "string"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
		case "vars":
			tp.IncludeVars, found = val.(map[interface{}]interface{})
			if !found {
				return HenchErr(ErrWrongType(field, val, "map[interface{}]interface{}"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
		default:
			// We have a module
			params, found := val.(string)
			if !found {
				return HenchErr(ErrWrongType(field, val, "string"), log.Fields{
					"task":     tp.Name,
					"solution": "Make sure the field is of proper type",
				}, "")
			}
			if numModule > 0 {
				return HenchErr(fmt.Errorf("'%v' is an extra Module.", field), log.Fields{
					"task":     tp.Name,
					"solution": "There can only be one module per task",
				}, "")
			}

			tp.Module, err = NewModule(field, params)
			if err != nil {
				return HenchErr(err, log.Fields{
					"task": tp.Name,
				}, fmt.Sprintf("Module '%v'", field))
			}
			numModule++
		}
	}

	return nil
}

// Checks the a slice of TaskProxy ptrs passed in by a Plan and determines
// whether if it's an include value or a normal task.  If it's a normal task
// it appends it as a standard task, otherwise it recursively expands the include
// statement
// NOTE: making this a function of PlanProxy in case we want to have more plan level variables
//       in the future.  Removes the need to pass each variable as a parameter.
func (px PlanProxy) PreprocessTasks() ([]*Task, error) {
	tasksList, err := parseTaskProxies(&px, nil, "")
	if err != nil {
		return nil, err
	}

	return tasksList, nil
}

// handles the include statements
func preprocessTasksHelper(taskFileName string, prevVars VarsMap, prevWhen string, px *PlanProxy) ([]*Task, error) {
	buf, err := ioutil.ReadFile(taskFileName)
	if err != nil {
		return nil, HenchErr(err, log.Fields{
			"solution": "Verify the included task file exists",
		}, "")
	}

	var tmpPx PlanProxy
	err = yaml.Unmarshal(buf, &tmpPx)
	if err != nil {
		return nil, HenchErr(err, log.Fields{
			"task_file": taskFileName,
			"solution":  "Check for Yaml formatting errors.  Usually indentation with tabs",
		}, "Unmarshalling Included Task")
	}

	// since it's using the included task file proxy
	// gotta assign the original plan level variables
	tmpPx.Sudo = px.Sudo
	return parseTaskProxies(&tmpPx, prevVars, prevWhen)
}

// Majority of the work done here.  Assigns and creates the Task list
func parseTaskProxies(px *PlanProxy, prevVars VarsMap, prevWhen string) ([]*Task, error) {
	var tasks []*Task
	for _, tp := range px.TaskProxies {
		task := Task{}
		// links when paramter
		// put out here b/c every task can have a when
		if tp.When != "" && prevWhen != "" {
			tp.When = tp.When + " && " + prevWhen
		} else if prevWhen != "" {
			tp.When = prevWhen
		}

		if tp.Include != "" {
			// links previous vars
			if prevVars != nil {
				MergeMap(prevVars, tp.IncludeVars, false)
			}

			includedTasks, err := preprocessTasksHelper(tp.Include, tp.IncludeVars, tp.When, px)
			if err != nil {
				return nil, HenchErr(err, log.Fields{
					"task":          tp.Name,
					"while_parsing": "include",
				}, "While checking Include")
			}

			tasks = append(tasks, includedTasks...)
		} else {
			if tp.Module == nil {
				return nil, HenchErr(fmt.Errorf("This task doesn't have a valid module"), log.Fields{
					"task":     tp.Name,
					"solution": "Each task needs to have exactly one module",
				}, "")
			}
			task.Name = tp.Name
			task.Module = tp.Module
			task.IgnoreErrors = tp.IgnoreErrors
			task.Local = tp.Local
			task.Register = tp.Register
			task.When = tp.When

			// NOTE: plan.Vars is merged in plan.execute(...) before Render
			// Creates an empty map for later use if there are no included vars
			if prevVars == nil {
				task.Vars = make(VarsMap)
			} else {
				task.Vars = prevVars
			}

			// takes plan sudo, changes it to task Sudo if there is one
			task.Sudo = px.Sudo
			if tp.SudoState != "" {
				var err error
				task.Sudo, err = strconv.ParseBool(tp.SudoState)
				if err != nil {
					return nil, HenchErr(err, log.Fields{
						"task":          tp.Name,
						"while_parsing": "sudo",
						"solution":      "Verify the 'sudo' field is a boolean",
					}, "")
				}
			}
			tasks = append(tasks, &task)
		}
	}

	return tasks, nil
}

// Processes plan level vars with includes
// All plan level vars will be in the vars map
// And any repeat vars in the includes will be a FCFS priority
// NOTE: if the user has multipl include blocks it'll grab the one closest to
//       the bottom
func (px PlanProxy) PreprocessVars() (VarsMap, error) {
	newVars := px.VarsProxy.Vars

	// parses include statements in vars
	if fileList, present := px.VarsProxy.Vars["include"]; present {
		for _, fName := range fileList.([]interface{}) {
			tempVars, err := preprocessVarsHelper(fName)
			if err != nil {
				return nil, HenchErr(err, log.Fields{"while_parsing": "include"}, "While checking includes")
			}
			MergeMap(tempVars, newVars, false)
		}
	}
	delete(newVars, "include")
	return newVars, nil
}

func preprocessVarsHelper(fName interface{}) (VarsMap, error) {
	newFName, found := fName.(string)
	if !found {
		return nil, HenchErr(ErrWrongType("Include", fName, "string"), log.Fields{
			"solution": "Make sure it's not a map",
		}, "")
	}

	buf, err := ioutil.ReadFile(newFName)
	if err != nil {
		return nil, HenchErr(err, log.Fields{
			"file":     newFName,
			"solution": "verify if file exists",
		}, "")
	}

	var px PlanProxy
	err = yaml.Unmarshal(buf, &px)
	if err != nil {
		return nil, HenchErr(err, log.Fields{
			"vars_file": fName,
			"solution":  "Check for Yaml formatting errors.  Usually indentation with tabs",
		}, "Unmarshalling Included Vars")
	}

	return px.VarsProxy.Vars, nil
}

// Process hosts list.  Checks the host list to see if any of the
// hosts entries are valid sections and will extract it based on
func filterInventory(groups []string, fullInventory Inventory) Inventory {
	// FIXME: Support globbing in the groups
	// No groups? No problem. Just return the full inventory
	//	return fullInventory
	if len(groups) == 0 {
		return fullInventory
	} else {
		filtered := Inventory{}
		filtered.Groups = make(map[string]HostGroup)
		//filtered.HostVars = fullInventory.HostVars
		for _, group := range groups {
			machines, present := fullInventory.Groups[group]
			if present {
				filtered.Groups[group] = machines
			}
		}
		filtered.HostVars = fullInventory.HostVars
		return filtered
	}
}

// Creates new plan proxy
func newPlanProxy(buf []byte) (PlanProxy, error) {
	var px PlanProxy
	err := yaml.Unmarshal(buf, &px)
	if err != nil {
		return px, err
	}
	return px, nil
}

// FIXME: it'll return a false error if there's an error parsing plan
// NOTE: for now throwing in a hardcoded error
func GetInventoryGroups(buf []byte) ([]string, error) {
	px, err := newPlanProxy(buf)
	if err != nil {
		return nil, HenchErr(err, nil, "Could not find inventory groups")
	}
	return px.InventoryGroups, nil

}

// For Plan
// NOTE: inventory should always be initialized and passed in?
//       or should we just check to see if it's nil?
func PreprocessPlan(buf []byte, inv Inventory) (*Plan, error) {
	px, err := newPlanProxy(buf)
	if err != nil {
		return nil, HenchErr(err, nil, "")
	}

	plan := Plan{}
	plan.Inventory = inv
	plan.Name = px.Name

	//common vars processing
	vars := make(VarsMap)
	if px.VarsProxy != nil {
		vars, err = px.PreprocessVars()
		if err != nil {
			return nil, HenchErr(err, log.Fields{
				"while_processing": "vars",
			}, "Error processing vars")
		}
	}
	plan.Vars = vars

	tasks, err := px.PreprocessTasks()
	if err != nil {
		return nil, HenchErr(err, log.Fields{
			"while_processing": "tasks",
		}, "Error processing tasks")
	}
	plan.Tasks = tasks

	return &plan, nil
}
