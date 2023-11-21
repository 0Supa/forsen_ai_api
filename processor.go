package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"app/ai"
	"app/conns"
	"app/db"
	"app/slg"
	"app/tts"
	"app/twitch"

	lua "github.com/yuin/gopher-lua"
)

type LuaConfig struct {
	MaxScriptExecTime time.Duration  `yaml:"max_script_exec_time"`
	MaxFuncCalls      map[string]int `yaml:"max_func_calls"`
}

type Processor struct {
	luaCfg *LuaConfig

	ai  *ai.Client
	tts *tts.Client
}

func NewProcessor(luacfg *LuaConfig, ai *ai.Client, tts *tts.Client) *Processor {
	return &Processor{
		luaCfg: luacfg,

		ai:  ai,
		tts: tts,
	}
}

func (p *Processor) Process(ctx context.Context, updates chan struct{}, eventWriter conns.EventWriter, user string) error {
	ctx, cancel := context.WithCancel(ctx)

	defer func() {
		cancel()
		if r := recover(); r != nil {
			slg.GetSlog(ctx).Error("connection panic", "user", user, "r", r, "stack", string(debug.Stack()))
		}
	}()

	go func() {
		<-updates
		slg.GetSlog(ctx).Info("processor signal recieved")
		cancel()
	}()

	settings, err := db.GetDbSettings(user)
	if err != nil {
		slg.GetSlog(ctx).Info("settings not found, defaulting")
		settings = &db.Settings{
			LuaScript: `
while true do
	user, msg, reward_id = get_next_event()
	tts(msg)
end
			`,
		}
	}

	slg.GetSlog(ctx).Info("Settings fetched", "settings", settings)

	luaState := lua.NewState(lua.Options{
		SkipOpenLibs:        true,
		IncludeGoStackTrace: true,
	})

	twitchChatCh := twitch.MessagesFetcher(ctx, user)

	luaState.SetGlobal("ai", luaState.NewFunction(func(l *lua.LState) int {
		request := l.Get(1).String()

		aiResponse, err := p.ai.Ask(ctx, 0, request)
		if err != nil {
			l.Push(lua.LString("ai request error: " + err.Error()))
			return 1
		}

		l.Push(lua.LString(aiResponse))
		return 1
	}))

	luaState.SetGlobal("text", luaState.NewFunction(func(l *lua.LState) int {
		request := l.Get(1).String()

		eventWriter(&conns.DataEvent{
			EventType: conns.EventTypeText,
			EventData: []byte(request),
		})

		return 0
	}))

	luaState.SetGlobal("tts", luaState.NewFunction(func(l *lua.LState) int {
		request := l.Get(1).String()

		ttsResponse, err := p.tts.TTS(ctx, request, nil)
		if err != nil {
			l.Push(lua.LString("tts request error: " + err.Error()))
			return 1
		}

		eventWriter(&conns.DataEvent{
			EventType: conns.EventTypeAudio,
			EventData: ttsResponse,
		})

		return 0
	}))

	luaState.SetGlobal("get_next_event", luaState.NewFunction(func(l *lua.LState) int {
		select {
		case msg := <-twitchChatCh:
			slg.GetSlog(ctx).Info("pushing", "msg", msg)
			l.Push(lua.LString(msg.UserName))
			l.Push(lua.LString(msg.Message))
			l.Push(lua.LString(msg.CustomRewardID))
		case <-ctx.Done():
			luaState.Close()
			return 0
		}

		return 3
	}))

	if err := luaState.DoString(settings.LuaScript); err != nil {
		return fmt.Errorf("lua execution err: %w", err)
	}

	slg.GetSlog(ctx).Info("processor is closing")

	return nil
}
