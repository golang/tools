package runtime

func GC()

func SetFinalizer(obj, finalizer any)

func Caller(skip int) (pc uintptr, file string, line int, ok bool)
