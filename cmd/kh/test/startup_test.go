package test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Exercise outside-tmux startup through a real terminal and a private server.
// Old sessions must survive, but must never swallow a new invocation's args.
func TestInteractiveStartupCreatesFreshSessions(t *testing.T) {
	for _, tool := range []string{"tmux", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " needed for terminal integration")
		}
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import os,pty,select,subprocess,sys,time,fcntl,termios,struct,shlex,json
binary,home=sys.argv[1:]
socket='kh_startup_'+str(os.getpid())
real_tmux=subprocess.check_output(['which','tmux']).decode().strip()
wrapper=os.path.join(home,'tmux')
with open(wrapper,'w') as f:
    f.write('#!/bin/sh\nexec '+shlex.quote(real_tmux)+' -L '+shlex.quote(socket)+' "$@"\n')
os.chmod(wrapper,0o700)
env=dict(os.environ,HOME=home,PATH=home+os.pathsep+os.environ['PATH'],TERM='xterm-256color',KH_AUTO_REBUILD='0',KH_PROVIDER='',KH_MODEL='',KH_EFFORT='',KH_AGENT='',KH_PARENT='',KH_AUTO='')
env.pop('TMUX',None)
env.pop('TMUX_PANE',None)
os.makedirs(os.path.join(home,'.kh'))
with open(os.path.join(home,'.kh','config.json'),'w') as f:
    f.write('{"memory_model":"off"}')
def tmux(*args):
    return subprocess.check_output([wrapper,*args],env=env,stderr=subprocess.STDOUT).decode().strip()
clients=[]
masters=[]
try:
    tmux('-f','/dev/null','new-session','-d','-s','kh','-n','main','sleep 60')
    old=tmux('display-message','-p','-t','kh:main','#{pane_id}')
    previous={'kh'}
    for index,args in enumerate([[],['-i','--auto','-model','startup-test-model',"let's do something"]]):
        cwd=os.path.join(home,'project '+str(index))
        os.mkdir(cwd)
        master,slave=pty.openpty()
        masters.append(master)
        fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',30,140,0,0))
        client=subprocess.Popen([binary,*args],cwd=cwd,env=env,stdin=slave,stdout=slave,stderr=slave,start_new_session=True)
        clients.append(client)
        os.close(slave)
        output=bytearray()
        def wait(check):
            end=time.monotonic()+7
            while not check():
                if time.monotonic()>end: raise AssertionError(bytes(output))
                if select.select([master],[],[],.05)[0]:
                    try: output.extend(os.read(master,65536))
                    except OSError: raise AssertionError(bytes(output))
        wait(lambda:len(set(tmux('list-sessions','-F','#{session_name}').splitlines())-previous)==1)
        sessions=set(tmux('list-sessions','-F','#{session_name}').splitlines())
        name=(sessions-previous).pop()
        assert name.startswith('kh-'),name
        pane=tmux('display-message','-p','-t',name+':main','#{pane_id}')
        def screen(): return tmux('capture-pane','-p','-t',pane)
        wait(lambda:'kh chat, session' in screen() and tmux('display-message','-p','-t',pane,'#{session_attached}')=='1')
        assert os.path.realpath(tmux('display-message','-p','-t',pane,'#{pane_current_path}'))==os.path.realpath(cwd)
        assert tmux('display-message','-p','-t',pane,'#{@kh_name}')=='main'
        command=tmux('display-message','-p','-t',pane,'#{pane_start_command}')
        if command.startswith('"'): command=json.loads(command)
        if args:
            assert shlex.split(command)[-len(args):]==args,command
            wait(lambda:"let's do something" in screen() and 'not logged in' in screen())
            assert 'startup-test-model' in screen(),screen()
        assert tmux('display-message','-p','-t','kh:main','#{pane_id}')==old
        # Closing a client must leave this chat available for explicit reattach.
        tmux('detach-client','-s',name)
        wait(lambda:client.poll() is not None)
        assert client.returncode==0,(client.returncode,bytes(output))
        assert set(tmux('list-sessions','-F','#{session_name}').splitlines())==sessions
        previous=sessions
finally:
    subprocess.run([wrapper,'kill-server'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    for client in clients:
        if client.poll() is None: client.kill()
        client.wait()
    for master in masters: os.close(master)
`, binary, t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("interactive startup: %s: %v", out, err)
	}
}
