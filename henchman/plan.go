package henchman

import (
	"log"
	"sync"
)

type VarsMap map[interface{}]interface{}
type RegMap map[string]interface{}

type Plan struct {
	Name      string
	Inventory Inventory
	Vars      VarsMap
	Tasks     []*Task
}

const HENCHMAN_PREFIX = "henchman_"

func (plan *Plan) Execute(machines []*Machine) error {
	log.Printf("Executing plan `%s' on %d machines\n", plan.Name, len(machines))

	// FIXME: Don't use localhost
	wg := new(sync.WaitGroup)
	for _, _machine := range machines {
		machine := _machine
		wg.Add(1)
		//		machineVars := plan.Inventory.Groups[machine.Group].Vars
		// NOTE: need individual registerMap for each machine
		registerMap := make(RegMap)
		go func() {
			defer wg.Done()
			for _, task := range plan.Tasks {
				// copy of task.Vars. It'll be different for each machine
				vars := make(VarsMap)
				MergeMap(task.Vars, vars, true)
				MergeMap(machine.Vars, vars, true)
				vars["current_host"] = machine
				err := task.Render(vars, registerMap)
				if err != nil {
					log.Printf("Error Rendering Task: %v.  Received: %v\n", task.Name, err.Error())
					return
				}
				taskResult, err := task.Run(machine, vars, registerMap)
				if err != nil {
					log.Println(err)
					return
				}
				log.Println(taskResult.Output)
			}
		}()
	}
	wg.Wait()
	return nil
}
