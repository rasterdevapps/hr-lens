HR Lens desktop agent for Linux (machine-wide install)
======================================================

What it does: sends the HR Lens server the application in front, a screenshot every
5 minutes, a continuous screen recording, key-press and click counts (never which keys),
and a live view while a manager is watching. Nothing is captured while the screen is locked.

This installs the agent for EVERY user of the computer. Each person signs the computer in to
their OWN HR Lens account, so each person's activity is tracked under their own account.

Needs: 64-bit Linux with an X11 (Xorg) desktop session - Linux Mint, Ubuntu on Xorg,
MATE, XFCE. Wayland sessions are not supported.

Install (once per computer, with sudo):

    tar -xzf hrlens-agent-linux.tar.gz
    cd hrlens-agent-linux
    sudo ./install.sh

It asks for the server address, installs the program system-wide and makes it start at every
login. Then each person signs in once, in a terminal on their own desktop:

    /opt/hrlens/hrlens-agent register -server http://SERVER:8080 -email you@company.com
    /opt/hrlens/hrlens-agent run &      # start now, without logging out

Useful commands:
    /opt/hrlens/hrlens-agent status       which account this computer uses (for you)
    /opt/hrlens/hrlens-agent run          run in the terminal to see what it is doing
    sudo /opt/hrlens/hrlens-agent uninstall   stop it starting at login (whole computer)
    pkill -f 'hrlens-agent run'           stop your running copy now
