package otelnative

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"testing"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestNativeAssemblyContainsPanicAndGoexit(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			value, err := runNativeAssembly(func() (*int, error) {
				if mode == "goexit" {
					runtime.Goexit()
				}
				panic("native-assembly-secret-82917")
			})
			if value != nil || !errors.Is(err, ErrNativeAssembly) {
				t.Fatalf("result/error = %#v/%v", value, err)
			}
		})
	}
}

func TestNativeAssemblyContainsPanicNilWithNilRecover(t *testing.T) {
	const childKey = "FROSTGROVE_NATIVE_ASSEMBLY_PANIC_NIL"
	if os.Getenv(childKey) == "1" {
		value, err := runNativeAssembly(func() (*int, error) {
			panic(nil)
		})
		if value != nil || !errors.Is(err, ErrNativeAssembly) {
			t.Fatalf("result/error = %#v/%v", value, err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestNativeAssemblyContainsPanicNilWithNilRecover$", "-test.count=1")
	command.Env = append(os.Environ(), "GODEBUG=panicnil=1", childKey+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("panic(nil) boundary failed: %v\n%s", err, output)
	}
}

func TestHTTPServerAssemblyFailureDoesNotSealRoutes(t *testing.T) {
	providers := Providers{Tracer: tracenoop.NewTracerProvider(), Meter: metricnoop.NewMeterProvider()}
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			routes := NewHTTPRoutes()
			if err := routes.HandleFunc("GET /before", func(http.ResponseWriter, *http.Request) {}); err != nil {
				t.Fatal(err)
			}
			option := HTTPServerOption(func(*httpServerSettings) {
				if mode == "goexit" {
					runtime.Goexit()
				}
				panic("http-option-secret-64821")
			})
			handler, err := HTTPServer(providers, routes, TrustedIngress, option)
			if handler != nil || !errors.Is(err, ErrNativeAssembly) {
				t.Fatalf("handler/error = %#v/%v", handler, err)
			}
			if err := routes.HandleFunc("GET /after", func(http.ResponseWriter, *http.Request) {}); err != nil {
				t.Fatalf("route table was sealed after failed assembly: %v", err)
			}
		})
	}
}

func TestMetricRegistrationDoesNotExposeNativeRegistration(t *testing.T) {
	var registration *MetricRegistration
	if _, exposed := any(registration).(metric.Registration); exposed {
		t.Fatal("guarded registration exposes native unregister path")
	}
}
