package worker

import (
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"slices"
	"strings"
	"sync"
	"time"
)

type (
	Worker struct {
		m           sync.RWMutex
		maxWorkers  int
		inputTasks  chan Task
		queuedTasks []Task
		toDoTasks   chan Task
	}

	Task struct {
		Key string // key to identify Task
		Do  func()
		// CheckIfNeedToDo returns if it needs to do now, not nil timeToDo is time to do Task if not now
		CheckIfNeedToDo func() (need bool, nextTimeToDo *time.Time)
		TimeToDo        time.Time
		RepeatDuration  time.Duration
	}

	TaskCreator interface {
		WithKey(key ...string) TaskCreator
		WithDo(do func()) TaskCreator
		WithCheckIfNeedToDo(checkIfNeedToDo func() (bool, *time.Time)) TaskCreator
		WithTimeToDo(timeToDo time.Time) TaskCreator
		WithRepeatDuration(repeatDuration time.Duration) TaskCreator
		Create() Task
	}
)

func NewTask() TaskCreator {
	return Task{}
}

func (t Task) WithKey(key ...string) TaskCreator {
	t.Key = strings.Join(key, "_")
	return t
}

func (t Task) WithDo(do func()) TaskCreator {
	t.Do = do
	return t
}

func (t Task) WithCheckIfNeedToDo(checkIfNeedToDo func() (bool, *time.Time)) TaskCreator {
	t.CheckIfNeedToDo = checkIfNeedToDo
	return t
}

func (t Task) WithTimeToDo(timeToDo time.Time) TaskCreator {
	t.TimeToDo = timeToDo
	return t
}

func (t Task) WithRepeatDuration(repeatDuration time.Duration) TaskCreator {
	t.RepeatDuration = repeatDuration
	return t
}

func (t Task) Create() Task {
	// if key is not set, set it unique key based on uuid v7
	if t.Key == "" {
		t.Key = uuid.Must(uuid.NewV7()).String()
	}
	// if time to do is not set, set it to now
	if t.TimeToDo.IsZero() {
		t.TimeToDo = time.Now()
	}
	// if checkIfNeedToDo is not set, set it to always need to do
	if t.CheckIfNeedToDo == nil {
		t.CheckIfNeedToDo = func() (need bool, nextTimeToDo *time.Time) {
			return true, nil
		}
	}
	return t
}

func NewWorker(maxWorkers int) *Worker {
	w := &Worker{
		maxWorkers:  maxWorkers,
		inputTasks:  make(chan Task, 10),
		toDoTasks:   make(chan Task, 200),
		queuedTasks: make([]Task, 0),
	}

	w.start()

	return w
}

func (d *Worker) start() {
	go d.manageTasks()
	go d.manageToDoTasks()
	go d.runWorkerPool()
}

func (d *Worker) AddTask(task Task) {
	d.inputTasks <- task
}

func (d *Worker) manageTasks() {
	for t := range d.inputTasks {
		log.Debug().Msg("New Task added")
		// delete Task with same key if exists
		d.deleteTasksByKey(t.Key)
		d.m.Lock()
		d.queuedTasks = append(d.queuedTasks, t)

		slices.SortFunc(d.queuedTasks, func(i, j Task) int {
			return i.TimeToDo.Compare(j.TimeToDo)
		})

		d.m.Unlock()
	}
}

func (d *Worker) manageToDoTasks() {
	for {
		d.m.Lock()
		if len(d.queuedTasks) > 0 && d.queuedTasks[0].TimeToDo.Before(time.Now()) {
			log.Debug().Msg("Task moved to toDoTasks")
			// move Task to toDoTasks
			d.toDoTasks <- d.queuedTasks[0]
			// remove Task from queuedTasks
			d.queuedTasks = d.queuedTasks[1:]
		}
		d.m.Unlock()
	}
}

func (d *Worker) runWorkerPool() {
	for i := 0; i < d.maxWorkers; i++ {
		go func(workerID int) {
			for task := range d.toDoTasks {
				log.Debug().Msgf("Worker %d started Task", workerID)
				// check if Task is repeatable then add to queue with new time and do Task
				if task.RepeatDuration > 0 {
					log.Debug().Msgf("Worker %d Task is repeatable", workerID)
					// if Task is repeatable, add to queue with new time
					task.TimeToDo = time.Now().Add(task.RepeatDuration)
					d.AddTask(task)
				}
				// if Task is not repeatable, do Task and check if it's needed to do again
				// check if Task is needed to be done
				need, nextTimeToDo := task.CheckIfNeedToDo()
				log.Debug().Msgf("Worker %d need: %t, nextTimeToDo: %v", workerID, need, nextTimeToDo)
				// do Task if it's needed
				if need {
					log.Debug().Msgf("Worker %d does Task", workerID)
					task.Do()
					log.Debug().Msgf("Worker %d Task done", workerID)
				} else {
					// if Task is not needed to do now, but new time, update Task time and add to queue
					if !(nextTimeToDo == nil) {
						log.Debug().Msgf("Worker %d Task is not needed to do now, but new time, update Task time and add to queue", workerID)
						task.TimeToDo = *nextTimeToDo
						d.AddTask(task)
					}
				}
			}
		}(i + 1)
	}
}

func (d *Worker) deleteTasksByKey(key string) {
	d.m.Lock()
	defer d.m.Unlock()
	newQueuedTasks := make([]Task, 0)
	for i := 0; i < len(d.queuedTasks); i++ {
		if d.queuedTasks[i].Key != key {
			newQueuedTasks = append(newQueuedTasks, d.queuedTasks[i])
		}
	}
	d.queuedTasks = newQueuedTasks
}
