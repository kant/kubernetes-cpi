// This file was generated by counterfeiter
package fakes

import "sync"

type ClientContext struct {
	ContextStub        func() string
	contextMutex       sync.RWMutex
	contextArgsForCall []struct{}
	contextReturns     struct {
		result1 string
	}
	NamespaceStub        func() string
	namespaceMutex       sync.RWMutex
	namespaceArgsForCall []struct{}
	namespaceReturns     struct {
		result1 string
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *ClientContext) Context() string {
	fake.contextMutex.Lock()
	fake.contextArgsForCall = append(fake.contextArgsForCall, struct{}{})
	fake.recordInvocation("Context", []interface{}{})
	fake.contextMutex.Unlock()
	if fake.ContextStub != nil {
		return fake.ContextStub()
	} else {
		return fake.contextReturns.result1
	}
}

func (fake *ClientContext) ContextCallCount() int {
	fake.contextMutex.RLock()
	defer fake.contextMutex.RUnlock()
	return len(fake.contextArgsForCall)
}

func (fake *ClientContext) ContextReturns(result1 string) {
	fake.ContextStub = nil
	fake.contextReturns = struct {
		result1 string
	}{result1}
}

func (fake *ClientContext) Namespace() string {
	fake.namespaceMutex.Lock()
	fake.namespaceArgsForCall = append(fake.namespaceArgsForCall, struct{}{})
	fake.recordInvocation("Namespace", []interface{}{})
	fake.namespaceMutex.Unlock()
	if fake.NamespaceStub != nil {
		return fake.NamespaceStub()
	} else {
		return fake.namespaceReturns.result1
	}
}

func (fake *ClientContext) NamespaceCallCount() int {
	fake.namespaceMutex.RLock()
	defer fake.namespaceMutex.RUnlock()
	return len(fake.namespaceArgsForCall)
}

func (fake *ClientContext) NamespaceReturns(result1 string) {
	fake.NamespaceStub = nil
	fake.namespaceReturns = struct {
		result1 string
	}{result1}
}

func (fake *ClientContext) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.contextMutex.RLock()
	defer fake.contextMutex.RUnlock()
	fake.namespaceMutex.RLock()
	defer fake.namespaceMutex.RUnlock()
	return fake.invocations
}

func (fake *ClientContext) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}
