package fmt

func Sprint(args ...any) string
func Sprintln(args ...any) string
func Sprintf(format string, args ...any) string

func Print(args ...any) (int, error)
func Println(args ...any)
func Printf(format string, args ...any) (int, error)

func Errorf(format string, args ...any) error
