package lua

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func field(l *State, key string, def int) int {
	l.Field(-1, key)
	r, ok := l.ToInteger(-1)
	if !ok {
		if def < 0 {
			Errorf(l, "field '%s' missing in date table", key)
		}
		r = def
	}
	l.Pop(1)
	return r
}

// strftime formats a time according to C strftime-style format specifiers.
func strftime(format string, t time.Time) (string, error) {
	var result []byte
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			result = append(result, format[i])
			continue
		}
		i++
		if i >= len(format) {
			return "", fmt.Errorf("invalid conversion specifier '%%'")
		}
		switch format[i] {
		case 'a':
			result = append(result, t.Format("Mon")...)
		case 'A':
			result = append(result, t.Format("Monday")...)
		case 'b', 'h':
			result = append(result, t.Format("Jan")...)
		case 'B':
			result = append(result, t.Format("January")...)
		case 'c':
			result = append(result, t.Format("Mon Jan  2 15:04:05 2006")...)
		case 'd':
			result = append(result, fmt.Sprintf("%02d", t.Day())...)
		case 'e':
			result = append(result, fmt.Sprintf("%2d", t.Day())...)
		case 'H':
			result = append(result, fmt.Sprintf("%02d", t.Hour())...)
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			result = append(result, fmt.Sprintf("%02d", h)...)
		case 'j':
			result = append(result, fmt.Sprintf("%03d", t.YearDay())...)
		case 'm':
			result = append(result, fmt.Sprintf("%02d", int(t.Month()))...)
		case 'M':
			result = append(result, fmt.Sprintf("%02d", t.Minute())...)
		case 'n':
			result = append(result, '\n')
		case 'p':
			if t.Hour() < 12 {
				result = append(result, "AM"...)
			} else {
				result = append(result, "PM"...)
			}
		case 'S':
			result = append(result, fmt.Sprintf("%02d", t.Second())...)
		case 't':
			result = append(result, '\t')
		case 'U':
			// Week number (Sunday as first day of week), 00-53
			yday := t.YearDay()
			wday := int(t.Weekday())
			result = append(result, fmt.Sprintf("%02d", (yday+6-wday)/7)...)
		case 'w':
			result = append(result, fmt.Sprintf("%d", int(t.Weekday()))...)
		case 'W':
			// Week number (Monday as first day of week), 00-53
			yday := t.YearDay()
			wday := int(t.Weekday())
			if wday == 0 {
				wday = 6
			} else {
				wday--
			}
			result = append(result, fmt.Sprintf("%02d", (yday+6-wday)/7)...)
		case 'x':
			result = append(result, fmt.Sprintf("%02d/%02d/%02d", int(t.Month()), t.Day(), t.Year()%100)...)
		case 'X':
			result = append(result, fmt.Sprintf("%02d:%02d:%02d", t.Hour(), t.Minute(), t.Second())...)
		case 'y':
			result = append(result, fmt.Sprintf("%02d", t.Year()%100)...)
		case 'Y':
			result = append(result, fmt.Sprintf("%04d", t.Year())...)
		case 'Z':
			name, _ := t.Zone()
			result = append(result, name...)
		case '%':
			result = append(result, '%')
		default:
			return "", fmt.Errorf("invalid conversion specifier '%%%c'", format[i])
		}
	}
	return string(result), nil
}

func osDate(l *State) int {
	format := OptString(l, 1, "%c")
	var t time.Time
	if l.IsNoneOrNil(2) {
		t = time.Now()
	} else {
		ts := CheckNumber(l, 2)
		t = time.Unix(int64(ts), 0)
	}

	// "!" prefix means UTC
	if len(format) > 0 && format[0] == '!' {
		format = format[1:]
		t = t.UTC()
	}

	// "*t" returns a table
	if format == "*t" {
		l.CreateTable(0, 9)
		l.PushInteger(t.Second())
		l.SetField(-2, "sec")
		l.PushInteger(t.Minute())
		l.SetField(-2, "min")
		l.PushInteger(t.Hour())
		l.SetField(-2, "hour")
		l.PushInteger(t.Day())
		l.SetField(-2, "day")
		l.PushInteger(int(t.Month()))
		l.SetField(-2, "month")
		l.PushInteger(t.Year())
		l.SetField(-2, "year")
		wday := int(t.Weekday()) + 1 // Lua: 1=Sunday, 7=Saturday
		l.PushInteger(wday)
		l.SetField(-2, "wday")
		l.PushInteger(t.YearDay())
		l.SetField(-2, "yday")
		l.PushBoolean(t.IsDST())
		l.SetField(-2, "isdst")
		return 1
	}

	result, err := strftime(format, t)
	if err != nil {
		Errorf(l, "%s", err.Error())
	}
	l.PushString(result)
	return 1
}

var osLibrary = []RegistryFunction{
	{"clock", clock},
	{"date", osDate},
	{"difftime", func(l *State) int {
		l.PushNumber(time.Unix(int64(CheckNumber(l, 1)), 0).Sub(time.Unix(int64(OptNumber(l, 2, 0)), 0)).Seconds())
		return 1
	}},

	// From the Lua manual:
	// "This function is equivalent to the ISO C function system"
	// https://www.lua.org/manual/5.2/manual.html#pdf-os.execute
	{"execute", func(l *State) int {
		c := OptString(l, 1, "")

		if c == "" {
			// Check whether "sh" is available on the system.
			err := exec.Command("sh").Run()
			l.PushBoolean(err == nil)
			return 1
		}

		terminatedSuccessfully := true
		terminationReason := "exit"
		terminationData := 0

		// Create the command.
		cmd := exec.Command("sh", "-c", c)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Run the command.
		if err := cmd.Run(); err != nil {
			terminatedSuccessfully = false
			terminationReason = "exit"
			terminationData = 1

			if exiterr, ok := err.(*exec.ExitError); ok {
				if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
					if status.Signaled() {
						terminationReason = "signal"
						terminationData = int(status.Signal())
					} else {
						terminationData = status.ExitStatus()
					}
				} else {
					// Unsupported system?
				}
			} else {
				// From man 3 system:
				// "If a child process could not be created, or its
				// status could not be retrieved, the return value
				// is -1."
				terminationData = -1
			}
		}

		// Deal with the return values.
		if terminatedSuccessfully {
			l.PushBoolean(true)
		} else {
			l.PushNil()
		}

		l.PushString(terminationReason)
		l.PushInteger(terminationData)

		return 3
	}},
	{"exit", func(l *State) int {
		var status int
		if l.IsBoolean(1) {
			if !l.ToBoolean(1) {
				status = 1
			}
		} else {
			status = OptInteger(l, 1, status)
		}
		// if l.ToBoolean(2) {
		// 	Close(l)
		// }
		os.Exit(status)
		panic("unreachable")
	}},
	{"getenv", func(l *State) int { l.PushString(os.Getenv(CheckString(l, 1))); return 1 }},
	{"remove", func(l *State) int { name := CheckString(l, 1); return FileResult(l, os.Remove(name), name) }},
	{"rename", func(l *State) int { return FileResult(l, os.Rename(CheckString(l, 1), CheckString(l, 2)), "") }},
	// {"setlocale", func(l *State) int {
	// 	op := CheckOption(l, 2, "all", []string{"all", "collate", "ctype", "monetary", "numeric", "time"})
	// 	l.PushString(setlocale([]int{LC_ALL, LC_COLLATE, LC_CTYPE, LC_MONETARY, LC_NUMERIC, LC_TIME}, OptString(l, 1, "")))
	// 	return 1
	// }},
	{"time", func(l *State) int {
		if l.IsNoneOrNil(1) {
			l.PushNumber(float64(time.Now().Unix()))
		} else {
			CheckType(l, 1, TypeTable)
			l.SetTop(1)
			year := field(l, "year", -1)
			month := field(l, "month", -1)
			day := field(l, "day", -1)
			hour := field(l, "hour", 12)
			min := field(l, "min", 0)
			sec := field(l, "sec", 0)
			l.PushNumber(float64(time.Date(year, time.Month(month), day, hour, min, sec, 0, time.Local).Unix()))
		}
		return 1
	}},
	{"tmpname", func(l *State) int {
		f, err := os.CreateTemp("", "lua_")
		if err != nil {
			Errorf(l, "unable to generate a unique filename")
		}
		defer f.Close()
		l.PushString(f.Name())
		return 1
	}},
}

// OSOpen opens the os library. Usually passed to Require.
func OSOpen(l *State) int {
	NewLibrary(l, osLibrary)
	return 1
}
