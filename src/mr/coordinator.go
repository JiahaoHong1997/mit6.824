package mr

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)
import "net"
import "os"
import "net/rpc"
import "net/http"

type Coordinator struct {
	// Your definitions here.
	fileName       []string
	unfinishedFile []string
	nReducer       int
	lock           sync.Mutex

	workerNum    int
	workerDone   []chan int
	rejectResult []bool

	fileLock             sync.Mutex
	reduceFiles          [][]string
	unFinishedReduceFile [][]string
	finalFiles           []string
}

// Your code here -- RPC handlers for the worker to call.

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

// Hello: Construct RPC connection with workers and distribute workerNum to each worker
func (c *Coordinator) Hello(args *HelloArgs, reply *HelloReply) error {
	if args.X == "hongjiahao" {
		c.lock.Lock()
		defer c.lock.Unlock()
		reply.Y = c.workerNum
		c.workerDone[c.workerNum] = make(chan int)
		c.rejectResult = append(c.rejectResult, false)
		c.workerNum++

		if c.workerNum > len(c.workerDone) {
			t := make([]chan int, 20)
			c.workerDone = append(c.workerDone, t...)
		}
		return nil
	}
	return errors.New("cannot build connection!")
}

// MapTask: Distribute map task to workers and enable timer function
func (c *Coordinator) MapTask(args *MapArgs, reply *MapReply) error {

	c.lock.Lock()
	defer c.lock.Unlock()
	if len(c.fileName) > 0 {
		n := len(c.fileName)
		reply.FileName = c.fileName[n-1]
		reply.NReducer = c.nReducer
		reply.Finished = false

		c.unfinishedFile = append(c.unfinishedFile, c.fileName[n-1])
		c.fileName = c.fileName[:n-1]
		c.rejectResult[args.WorkerNum] = false

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		go ifMapFinished(c, ctx, cancel, reply.FileName, args.WorkerNum)
	} else if len(c.unfinishedFile) > 0 {
		reply.FileName = ""
		reply.NReducer = c.nReducer
		reply.Finished = false
	} else {
		reply.FileName = ""
		reply.NReducer = c.nReducer
		reply.Finished = true
	}
	return nil
}

// FinishMap: Reply to the completion task response of workers, and determine whether the map task timed out due to rpc latency
func (c *Coordinator) FinishMap(args *FinishMapArgs, reply *FinishMapReply) error {

	if args.X == false {
		return nil
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	// If get the reduceFiles within 10s but overtime because of RPC latency
	if c.rejectResult[args.WorkerNum] {
		c.rejectResult[args.WorkerNum] = false
		return nil
	}

	c.workerDone[args.WorkerNum] <- 1

	for i := 0; i < c.nReducer; i++ {
		c.reduceFiles[i] = append(c.reduceFiles[i], args.FileName[i])
	}

	reply.Y = 1

	return nil
}

func (c *Coordinator) ReduceTask(args *ReduceTaskArgs, reply *ReduceTaskReply) error {
	if len(c.reduceFiles) > 0 {
		c.lock.Lock()
		defer c.lock.Unlock()
		n := len(c.reduceFiles)
		reply.ReducerFile = c.reduceFiles[n-1]
		reply.Finished = false
		c.unFinishedReduceFile = append(c.unFinishedReduceFile, c.reduceFiles[n-1])
		c.reduceFiles = c.reduceFiles[:n-1]

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		go ifReduceFinished(c, ctx, cancel, reply.ReducerFile, args.WorkerNum)
	} else if len(c.unFinishedReduceFile) > 0 {
		reply.Finished = false
		reply.ReducerFile = nil
	} else {
		reply.Finished = true
		reply.ReducerFile = nil
	}

	return nil
}

func (c *Coordinator) FinishReduce(args *FinishReduceArgs, reply *FinishReduceReply) error {
	if args.X == false {
		return nil
	}

	c.fileLock.Lock()
	defer c.fileLock.Unlock()

	c.workerDone[args.WorkerNum] <- 1
	c.finalFiles = append(c.finalFiles, args.File)

	reply.Y = 1

	return nil
}

// ifMapFinished: Monitor whether the map task times out and update the task status
func ifMapFinished(c *Coordinator, ctx context.Context, cancel context.CancelFunc, filename string, workerNum int) {

	select {
	case <-ctx.Done():
		c.lock.Lock()
		defer c.lock.Unlock()
		for i := 0; i < len(c.unfinishedFile); i++ {
			if c.unfinishedFile[i] == filename {
				c.unfinishedFile = append(c.unfinishedFile[:i], c.unfinishedFile[i+1:]...)
				c.fileName = append(c.fileName, filename)
				c.rejectResult[workerNum] = true
				return
			}
		}
	case <-c.workerDone[workerNum]:
		c.lock.Lock()
		defer c.lock.Unlock()

		for i := 0; i < len(c.unfinishedFile); i++ {
			if c.unfinishedFile[i] == filename {
				c.unfinishedFile = append(c.unfinishedFile[:i], c.unfinishedFile[i+1:]...)
			}
		}
	}
	cancel()
}

func ifReduceFinished(c *Coordinator, ctx context.Context, cancel context.CancelFunc,
	reduceFiles []string, workerNum int) {

	select {
	case <-ctx.Done():
		c.lock.Lock()
		defer c.lock.Unlock()
		for i := 0; i < len(c.unFinishedReduceFile); i++ {
			if c.unFinishedReduceFile[i][0] == reduceFiles[0] {
				c.unFinishedReduceFile = append(c.unFinishedReduceFile[:i], c.unFinishedReduceFile[i+1:]...)
				c.reduceFiles = append(c.reduceFiles, reduceFiles)
				return
			}
		}
	case <-c.workerDone[workerNum]:
		c.lock.Lock()
		defer c.lock.Unlock()
		for i := 0; i < len(c.unFinishedReduceFile); i++ {
			if c.unFinishedReduceFile[i][0] == reduceFiles[0] {
				c.unFinishedReduceFile = append(c.unFinishedReduceFile[:i], c.unFinishedReduceFile[i+1:]...)
			}
		}
	}
	cancel()
}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	ret := false

	// Your code here.

	return ret
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {

	workerDone := make([]chan int, 20)

	c := Coordinator{
		fileName: files,
		nReducer: nReduce,

		workerDone:  workerDone,
		reduceFiles: make([][]string, nReduce),
	}

	// Your code here.

	c.server()
	return &c
}
