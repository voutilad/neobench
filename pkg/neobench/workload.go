package neobench

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"sort"
	"time"
)

type Workload struct {
	// set on command line and built in
	Variables map[string]interface{}

	Scripts Scripts

	Rand *rand.Rand
}

// Scripts in a workload, and utilities to draw a weighted random script
type Scripts struct {
	// Scripts sorted by weight
	Scripts []Script
	// Lookup table for choice of scripts; one entry for each script, each entry records the cumulative
	// weight of that script and all scripts before it in the array. See Choose() for details
	WeightedLookup []int
	// Sum of all weights in []Script
	TotalWeight int
}

func NewScripts(scripts ...Script) Scripts {
	lookupTable := make([]int, len(scripts))
	cumulativeWeight := 0
	for i, script := range scripts {
		cumulativeWeight += int(script.Weight)
		lookupTable[i] = cumulativeWeight
	}

	return Scripts{
		Scripts:        scripts,
		WeightedLookup: lookupTable,
		TotalWeight:    cumulativeWeight,
	}
}

func (s *Scripts) Choose(r *rand.Rand) Script {
	// Common case: There is just one script
	if len(s.Scripts) == 1 {
		return s.Scripts[0]
	}

	// How do you take the uniformly random number we get from rand, and convert it into a weighted choice of
	// a script to use?
	//
	// Imagine that we create a segmented number line, each segment representing one script. The length of each
	// segment is the weight of that script. So for three scripts, A@2, B@3, C@3, we create a line like:
	//
	//   0 1 2 3 4 5 6 7 8 9
	//   [AA][BBBBBB][CCCCCC]
	//
	// Then we pick a number between 0 and the max of the number line (10 in the example). Say we get 4:
	//
	//   0 1 2 3 4 5 6 7 8 9
	//   [AA][BBBBBB][CCCCCC]
	//           ^
	//
	// The problem with this is that while it's easy visually to see which "item" we landed on, it's not obvious
	// how to do it quickly on a computer. The solution used here is to maintain a lookup table with the cumulative
	// weight at each segment, one entry per segment:
	//
	//   0 1 2 3 4 5 6 7 8 9
	//   [AA][BBBBBB][CCCCCC]
	//    +2     +3     +3    <-- weight of each segment
	//    2      5      8     <-- lookup table value (eg. cumulation of weights)
	//
	// We can then do binary search into the lookup table, the index we get back is the segment our number fell on.

	// 1: Pick a random number between 1 and the combined weight of all scripts
	point := r.Intn(s.TotalWeight) + 1

	// 2: Use binary search in the weighted lookup table to find the closest index for this weight
	index := sort.SearchInts(s.WeightedLookup, point)

	return s.Scripts[index]
}

type Script struct {
	Readonly bool
	Weight   uint
	Commands []Command
}

type ScriptContext struct {
	Stderr io.Writer
	Vars   map[string]interface{}
	Rand   *rand.Rand
}

// Evaluate this script in the given context
func (s *Script) Eval(ctx ScriptContext) (UnitOfWork, error) {
	uow := UnitOfWork{
		Readonly:   s.Readonly,
		Statements: nil,
	}

	for _, cmd := range s.Commands {
		if err := cmd.Execute(&ctx, &uow); err != nil {
			return uow, err
		}
	}

	return uow, nil
}

func (s *Workload) NewClient() ClientWorkload {
	return ClientWorkload{
		Variables: s.Variables,
		Scripts:   s.Scripts,
		Rand:      rand.New(rand.NewSource(s.Rand.Int63())),
		Stderr:    os.Stderr,
	}
}

type ClientWorkload struct {
	Readonly bool
	// variables set on command line and built-in
	Variables map[string]interface{}
	Scripts   Scripts
	Rand      *rand.Rand
	Stderr    io.Writer
}

func (s *ClientWorkload) Next() (UnitOfWork, error) {
	vars := make(map[string]interface{})
	for k, v := range s.Variables {
		vars[k] = v
	}

	script := s.Scripts.Choose(s.Rand)
	return script.Eval(ScriptContext{
		Stderr: s.Stderr,
		Vars:   vars,
		Rand:   s.Rand,
	})
}

type UnitOfWork struct {
	Readonly   bool
	Statements []Statement
}

type Statement struct {
	Query  string
	Params map[string]interface{}
}

type Command interface {
	Execute(ctx *ScriptContext, uow *UnitOfWork) error
}

type QueryCommand struct {
	Query string
}

func (c QueryCommand) Execute(ctx *ScriptContext, uow *UnitOfWork) error {
	params := make(map[string]interface{})
	for k, v := range ctx.Vars {
		params[k] = v
	}
	uow.Statements = append(uow.Statements, Statement{
		Query:  c.Query,
		Params: params,
	})
	return nil
}

type SetCommand struct {
	VarName    string
	Expression Expression
}

func (c SetCommand) Execute(ctx *ScriptContext, uow *UnitOfWork) error {
	value, err := c.Expression.Eval(ctx)
	if err != nil {
		return err
	}
	ctx.Vars[c.VarName] = value
	return nil
}

type SleepCommand struct {
	Duration Expression
	Unit     time.Duration
}

func (c SleepCommand) Execute(ctx *ScriptContext, uow *UnitOfWork) error {
	sleepNumber, err := c.Duration.Eval(ctx)
	if err != nil {
		return err
	}
	sleepInt, ok := sleepNumber.(int64)
	if !ok {
		return fmt.Errorf("\\sleep must be given an integer expression, got %v", sleepNumber)
	}
	time.Sleep(time.Duration(sleepInt) * c.Unit)
	return nil
}