package worker

import (
	"github.com/rs/zerolog/log"
	"slices"
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
		Do func()
		// CheckIfNeedToDo returns if it needs to do now, not nil timeToDo is time to do task if not now
		CheckIfNeedToDo func() (need bool, nextTimeToDo *time.Time)
		TimeToDo        time.Time
	}
)

func NewWorker(maxWorkers int) *Worker {
	return &Worker{
		maxWorkers:  maxWorkers,
		inputTasks:  make(chan Task, 10),
		toDoTasks:   make(chan Task, 200),
		queuedTasks: make([]Task, 0),
	}
}

func (d *Worker) Start() {
	go d.manageTasks()
	go d.manageToDoTasks()
	go d.runWorkerPool()
}

func (d *Worker) AddTask(task Task) {
	d.inputTasks <- task
}

func (d *Worker) manageTasks() {
	for task := range d.inputTasks {
		log.Debug().Msg("New task added")
		if task.TimeToDo.IsZero() {
			task.TimeToDo = time.Now()
		}
		d.m.Lock()
		d.queuedTasks = append(d.queuedTasks, task)

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
			d.toDoTasks <- d.queuedTasks[0]
			d.queuedTasks = d.queuedTasks[1:]
		}
		d.m.Unlock()
	}
}

func (d *Worker) runWorkerPool() {
	for i := 0; i < d.maxWorkers; i++ {
		go func(workerID int) {
			for task := range d.toDoTasks {
				log.Debug().Msgf("Worker %d started task", workerID)
				// check if task is needed to be done
				need, nextTimeToDo := task.CheckIfNeedToDo()
				log.Debug().Msgf("Worker %d need: %t, nextTimeToDo: %v", workerID, need, nextTimeToDo)
				// do task if it's needed
				if need {
					log.Debug().Msgf("Worker %d does task", workerID)
					task.Do()
					log.Debug().Msgf("Worker %d task done", workerID)
				} else {
					// if task is not needed to do now, but new time, update task time and add to queue
					if !(nextTimeToDo == nil) {
						log.Debug().Msgf("Worker %d task is not needed to do now, but new time, update task time and add to queue", workerID)
						task.TimeToDo = *nextTimeToDo
						d.AddTask(task)
					}
				}
			}
		}(i + 1)
	}
}
