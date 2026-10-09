package language

import (
	"runtime"
	"testing"
)

type frameTestExecutor struct{ execute func(*jitFrame, uint64) value }

func (e frameTestExecutor) run(f *jitFrame, id uint64) value { return e.execute(f, id) }
func (frameTestExecutor) close() error                       { return nil }

func TestLLVMReusedFramesReleaseRoots(t *testing.T) {
	// Keep the outer execution alive while a returned child's only root dies.
	// Reusing native frames must not retain its result/object graph.
	s := &jitExecution{id: jitFrameID.Add(1)}
	vm := &machine{}
	child := &preparedFunction{name: "child"}
	collected := make(chan struct{}, 1)
	s.code = frameTestExecutor{execute: func(f *jitFrame, _ uint64) value {
		if f.fn == child {
			func() {
				o := &objectValue{fields: []value{{kind: intValue, number: 42}}}
				runtime.SetFinalizer(o, func(*objectValue) { collected <- struct{}{} })
				f.result = value{kind: referenceValue, object: o}
			}()
			runtime.GC()
			select {
			case <-collected:
				t.Fatal("Collected active child root")
			default:
			}
			return value{}
		}
		for repeat := 0; repeat < 3; repeat++ {
			s.run(vm, child, 0)
			if s.current != f {
				t.Fatal("Did not restore caller frame")
			}
			freed := false
			for attempt := 0; attempt < 20; attempt++ {
				runtime.GC()
				select {
				case <-collected:
					freed = true
				default:
					runtime.Gosched()
				}
				if freed {
					break
				}
			}
			if !freed {
				t.Fatal("Returned child retained an object during active execution")
			}
		}
		return value{}
	}}
	jitFrames.Store(s.id, s)
	defer jitFrames.Delete(s.id)
	s.run(vm, &preparedFunction{name: "outer"}, 0)
	if s.current != nil || s.used != 0 || len(s.frames) != 2 {
		t.Fatal("Unbalanced reusable frames")
	}
}
