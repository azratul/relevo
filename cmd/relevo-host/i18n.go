package main

import "fmt"

// Terminal and tray texts. English by default; "es" via config "language"
// (set from the panel) or --lang.
var texts = map[string][2]string{ // key: {en, es}
	"panel":        {"Local panel:", "Panel local:"},
	"link":         {"Link:      ", "Enlace:    "},
	"password":     {"Password:  ", "Contraseña:"},
	"share":        {"(share them through different channels)", "(compártelos por canales distintos)"},
	"ctrlc":        {"Press Ctrl+C to end the session and quit.", "Ctrl+C para terminar la sesión y salir."},
	"how":          {"How will speakers connect?", "¿Cómo se conectarán los relatores?"},
	"own":          {"My own tunnel or proxy (Pangolin, frp, …): I will type its public URL", "Mi propio túnel o proxy (Pangolin, frp, …): escribiré su URL pública"},
	"option":       {"Option: ", "Opción: "},
	"unencrypted":  {" [UNENCRYPTED]", " [SIN CIFRAR]"},
	"confirmPlain": {"This tunnel does NOT encrypt: the password and commands travel in clear text. Type \"yes\" to continue: ", "Este túnel NO cifra: la contraseña y las órdenes viajarán en claro. Escribe «sí» para continuar: "},
	"publicURL":    {"Public URL (https://…): ", "URL pública (https://…): "},
	"localPort":    {"Local port your tunnel points to: ", "Puerto local al que apunta tu túnel: "},
	"noSession":    {"No session", "Sin sesión"},
	"openPanel":    {"Open panel", "Abrir panel"},
	"openPanelTip": {"Open the host panel", "Abrir el panel del anfitrión"},
	"pause":        {"Pause control", "Pausar control"},
	"resume":       {"Resume control", "Reanudar control"},
	"end":          {"End session", "Terminar sesión"},
	"quit":         {"Quit", "Salir"},
	"quitTip":      {"End the session and quit", "Terminar la sesión y salir"},
	"reconnecting": {"Reconnecting…", "Reconectando…"},
	"paused":       {"Paused", "En pausa"},
	"ready":        {"Ready", "Listo"},
	"notReady":     {"Not ready: ", "No listo: "},
	"speakers":     {" · speakers: ", " · relatores: "},
	"tooltip":      {"Relevo: presentation remote", "Relevo: control remoto de presentaciones"},
}

var lang = "en"

func T(key string) string {
	t, ok := texts[key]
	if !ok {
		return key
	}
	if lang == "es" {
		return t[1]
	}
	return t[0]
}

func Tf(key string, a ...any) string { return fmt.Sprintf(T(key), a...) }
