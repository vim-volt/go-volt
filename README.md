:zap: Volt
[![Travis CI - Build status](https://travis-ci.org/vim-volt/volt.svg?branch=master)](https://travis-ci.org/vim-volt/volt)
[![AppVeyor - Build status](https://ci.appveyor.com/api/projects/status/github/vim-volt/volt?branch=master&svg=true)](https://ci.appveyor.com/project/vim-volt/volt)
----

```
 .----------------.  .----------------.  .----------------.  .----------------.
| .--------------. || .--------------. || .--------------. || .--------------. |
| | ____   ____  | || |     ____     | || |   _____      | || |  _________   | |
| ||_  _| |_  _| | || |   .'    `.   | || |  |_   _|     | || | |  _   _  |  | |
| |  \ \   / /   | || |  /  .--.  \  | || |    | |       | || | |_/ | | \_|  | |
| |   \ \ / /    | || |  | |    | |  | || |    | |   _   | || |     | |      | |
| |    \ ' /     | || |  \  `--'  /  | || |   _| |__/ |  | || |    _| |_     | |
| |     \_/      | || |   `.____.'   | || |  |________|  | || |   |_____|    | |
| |              | || |              | || |              | || |              | |
| '--------------' || '--------------' || '--------------' || '--------------' |
 '----------------'  '----------------'  '----------------'  '----------------'
```

See [the command reference](https://github.com/vim-volt/volt/blob/master/CMDREF.md) for more details.


* [What is Volt](#what-is-volt)
* [Install](#install)
* [Self upgrade](#self-upgrade)
* [Introduction](#introduction)
  * [VOLTPATH](#voltpath)
  * [Install plugin(s)](#install-plugins)
  * [Update plugins](#update-plugins)
  * [Uninstall plugins](#uninstall-plugins)
* [How it works](#how-it-works)
  * [Syncing ~/.vim/pack/volt directory with $VOLTPATH](#syncing-vimpackvolt-directory-with-voltpath)
* [Config](#config)
* [Features](#features)
  * [Easy setup](#easy-setup)
  * [Configuration per plugin ("Plugconf" feature)](#configuration-per-plugin-plugconf-feature)
  * [Switch set of plugins ("Profile" feature)](#switch-set-of-plugins-profile-feature)
  * [Manage a local directory as a vim plugin](#manage-a-local-directory-as-a-vim-plugin)
* [Contribution](#tada-contribution)



## What is Volt

* Multi-platform CLI tool managing Vim plugin life
* Parallel vim plugin update
* Based on Vim 8 [packages feature](http://vimhelp.appspot.com/repeat.txt.html#packages)
    * All plugins are installed under `~/.vim/pack/volt` directory
* Zero overhead & optimizations
    * Volt does not slow down your startup
    * We plan to support transpilation of Vim script (Uglification, Transpiler to LuaJIT, ...)
* Provides version locking & [easy setup](#easy-setup)
* Managing plugin configuration by [plugconf](#configuration-per-plugin-plugconf-feature) files
    * This separates plugin configuration from vimrc, so you can easily disable/remove plugins in a second
* [Profile feature](#switch-set-of-plugins-profile-feature) saves set of plugins, vimrc, and gvimrc
    * You can switch those combinations with one command
    * For example, you can switch various Vim such as Web development mode, Essential plugins + vimrc only, or Vanilla Vim.

## Install

* **RECOMMENDED**: Download binaries from [GitHub releases](https://github.com/vim-volt/volt/releases)
* Or `go get github.com/vim-volt/volt`
  * You need Go 1.9 or higher
  * And if you are using Windows Subsystem Linux, you need to apply **[the patch for os.RemoveAll()](https://go-review.googlesource.com/c/go/+/62970) ! ([#1](https://github.com/vim-volt/go-volt/issues/1))**
  * But it's a hassle, you can just download linux-(386/amd64) binaries from [GitHub releases](https://github.com/vim-volt/volt/releases) :)

And there is bash completion script in [\_contrib](https://github.com/vim-volt/volt/blob/master/_contrib/completion/bash) directory (thanks @AvianY).

## Self upgrade

If you already have older version's `volt` command, you can use `volt self-upgrade` command.

```
$ volt self-upgrade
```

will upgrade current running volt binary to the latest version if the [newer releases](https://github.com/vim-volt/volt/releases) published.

Or also you can just checks if the newer releases published by running `volt self-upgrade -check`.

## Introduction

### VOLTPATH

You can change base directory of volt by `VOLTPATH` environment variable.
This is `$HOME/volt` by default.

### Install plugin(s)

For example, installing [tyru/caw.vim](https://github.com/tyru/caw.vim) plugin:

```
$ volt get https://github.com/tyru/caw.vim   # most verbose way (but handy when you copy & paste from browser address bar :)
$ volt get github.com/tyru/caw.vim           # you can omit https:// of repository URL
$ volt get tyru/caw.vim                      # you can omit github.com/ if the repository is on GitHub
```

And you can install multiple plugins (parallel download):

```
$ volt get tyru/open-browser.vim tyru/open-browser-github.vim
```

For example, what `volt get tyru/caw.vim` command does internally is:

* Clone and install the repository to `$VOLTPATH/repos/github.com/tyru/caw.vim`
    * Volt does not require `git` command because it's powered by [go-git](https://github.com/src-d/go-git)
* Update `$VOLTPATH/lock.json`
* Run `volt build`
    * Copy repository files to `~/.vim/pack/volt/opt/github.com_tyru_caw.vim`
    * Install `~/.vim/pack/volt/start/system/plugin/bundled_plugconf.vim`
        * It loads plugins like `packadd github.com_tyru_caw.vim`

### Update plugins

You can update all plugins as follows:

```
$ volt get -l -u
```

`-u` updates specified plugins.
`-l` works like all plugins in current profile are specified (the repositories list is read from `$VOLTPATH/lock.json`).

Or you can update only specified plugin(s) as follows:

```
$ volt get -u tyru/caw.vim
```

### Uninstall plugins

You can uninstall `tyru/caw.vim` as follows:

```
$ volt rm tyru/caw.vim   # (sob)
```

## How it works

### Syncing ~/.vim/pack/volt directory with $VOLTPATH

![volt build](https://raw.githubusercontent.com/vim-volt/volt/master/img/volt-build.png)

`volt build` synchronizes `~/.vim/pack/volt/...` with `$VOLTPATH/rc` (vimrc/gvimrc), `$VOLTPATH/repos` (repositories), `$VOLTPATH/plugconf` (plugconf):

1. Install `$VOLTPATH/rc/<profile>/{vimrc.vim,gvimrc.vim}` to `~/.vim/vimrc` and `~/.vim/gvimrc`
1. Copy `$VOLTPATH/repos/<repos>` to `~/.vim/pack/volt/opt/<repos>`
  * if `$VOLTPATH/repos/<repos>` has modified/new file(s), copy them to `~/.vim/pack/volt/opt/<repos>`
  * if `$VOLTPATH/repos/<repos>` does not exist, remove `~/.vim/pack/volt/opt/<repos>`
1. Install bootstrap script to `~/.vim/pack/volt/start/system/plugin/bundled_plugconf.vim` (load plugins & plugconfs)

Users don't have to run `volt build` when running `volt get`, `volt rm`, `volt add`, `volt profile`, ... commands, because those commands invoke `volt build` command internally if the commands modify repositories, plugconf, lock.json.
But if you edit `$VOLTPATH/rc/<profile>/vimrc.vim` or `$VOLTPATH/rc/<profile>/gvimrc.vim`, you have to run `volt build` to copy them to `~/.vim/vimrc` or `~/.vim/gvimrc`.

`volt build` uses cache for the next running.
Normally `volt build` synchronizes correctly, but if you met the bug, try `volt build -full` (or please [file an issue](https://github.com/vim-volt/volt/issues/new) as possible :) to ignore the previous cache.

## Config

Config file: `$VOLTPATH/config.toml`

```toml
[build]
# * "symlink" (default): "volt build" creates symlinks "~/.vim/pack/volt/opt/<repos>" referring to "$VOLTPATH/repos/<repos>"
# * "copy": "volt build" copies "$VOLTPATH/repos/<repos>" files to "~/.vim/pack/volt/opt/<repos>"
strategy = "symlink"

[get]
# * true (default): "volt get" creates skeleton plugconf file at "$VOLTPATH/plugconf/<repos>.vim"
# * false: It does not creates skeleton plugconf file
create_skeleton_plugconf = true

# * true (default): When "volt get" or "volt get -u" fail and "git" command is
#                   installed, it tries to execute "git clone" or "git pull" as a fallback
# * false: "volt get" or "volt get -u" won't try to execute fallback commands
fallback_git_cmd = true
```

## Features

### Easy setup

If you want to install set of plugins which you have installed by `volt get`, you can use `volt get -l`.

```
$ volt get -l   # install missing plugins in current profile in $VOLTPATH/lock.json
```

First, you have to manage the following files under `$VOLTPATH`.

```
$VOLTPATH/
├ ─ ─  lock.json
├ ─ ─  plugconf (optional)
└ ─ ─  rc (optional)
```

**NOTE: DO NOT RECOMMEND SHARING VOLT DIRECTORY ITSELF ON DROPBOX** (see [related issues](https://github.com/vim-volt/volt/issues?utf8=%E2%9C%93&q=is%3Aissue+dropbox)).

For example, my actual setup is:

```
$ tree -L 1 ~/volt/
/home/tyru/volt/
├ ─ ─  lock.json -> /home/tyru/git/dotfiles/dotfiles/volt/lock.json
├ ─ ─  plugconf -> /home/tyru/git/dotfiles/dotfiles/volt/plugconf
├ ─ ─  rc -> /home/tyru/git/dotfiles/dotfiles/volt/rc
└ ─ ─  repos
```

See [volt directory](https://github.com/tyru/dotfiles/tree/36456c73e66898c8a725e2043ff0ffcba941ebf4/dotfiles/volt) in [tyru/dotfiles](https://github.com/tyru/dotfiles/) repository for example.

### Configuration per plugin ("Plugconf" feature)

You can write plugin configuration in "plugconf" file.
The files are placed at:

* `$VOLTPATH/plugconf/<repository>.vim`

For example, [tyru/open-browser-github.vim](https://github.com/tyru/open-browser-github.vim) configuration is `$VOLTPATH/plugconf/github.com/tyru/open-browser.vim.vim` because "github.com/tyru/open-browser-github.vim" is the repository URL.

Some special functions can be defined in plugconf file:

* `s:config()`
    * Plugin configuration
* `s:loaded_on()` (optional)
    * Return value: String (when to load a plugin by `:packadd`)
    * This function specifies when to load a plugin by `:packadd`
    * e.g.: `return "start"` (default, load on `VimEnter` autocommand)
    * e.g.: `return "filetype=<filetype>"` (load on `FileType` autocommand)
    * e.g.: `return "excmd=<excmd>"` (load on `CmdUndefined` autocommand)
* `s:depends()` (optional)
    * Return value: List (repository name)
    * The specified plugins by this function are loaded before the plugin of plugconf
    * e.g.: `["github.com/tyru/open-browser.vim"]`

However, you can also define global functions in plugconf (see [tyru/nextfile.vim example](https://github.com/tyru/dotfiles/blob/36456c73e66898c8a725e2043ff0ffcba941ebf4/dotfiles/volt/plugconf/github.com/tyru/nextfile.vim.vim)).

An example config of [tyru/open-browser-github.vim](https://github.com/tyru/open-browser-github.vim):

```vim
" Plugin configuration like the code written in vimrc.
function! s:config()
  let g:openbrowser_github_always_use_commit_hash = 1
endfunction

" This function determines when a plugin is loaded.
"
" Possible values are:
" * 'start' (a plugin will be loaded at VimEnter event)
" * 'filetype=<filetypes>' (a plugin will be loaded at FileType event)
" * 'excmd=<excmds>' (a plugin will be loaded at CmdUndefined event)
" <filetypes> and <excmds> can be multiple values separated by comma.
"
" This function must contain 'return "<str>"' code.
" (the argument of :return must be string literal)
function! s:loaded_on()
  " this is the default value, you don't have to write this
  return 'start'
endfunction

" Dependencies of this plugin.
" The specified dependencies are loaded after this plugin is loaded.
"
" This function must contain 'return [<repos>, ...]' code.
" (the argument of :return must be list literal, and the elements are string)
function! s:depends()
  return ['github.com/tyru/open-browser.vim']
endfunction
```

NOTE:

* Plugconf file is parsed by [go-vimlparser](https://github.com/haya14busa/go-vimlparser)
* The rhs of `:return` must be literal
* Breaking newline by backslash (`\`) in `s:loaded_on()` and `s:depends()` is safe, but the following code can not be recognized (currently not supported at least)

```vim
" Wrong
function! s:loaded_on()
  let when = 'filetype=vim'
  return when
endfunction

" Wrong
function! s:depends()
  let list =  ['github.com/tyru/open-browser.vim']
  return list
endfunction

" OK
function! s:depends()
  return [
  \  'github.com/tyru/open-browser.vim'
  \]
endfunction
```

See [plugconf directory](https://github.com/tyru/dotfiles/tree/75a37b4a640a5cffecf34d2a52406d0f53ee6f09/dotfiles/volt/plugconf) in [tyru/dotfiles](https://github.com/tyru/dotfiles/) repository for example.

### Switch set of plugins ("Profile" feature)

You can think this is similar feature of **branch** of `git`.
The default profile name is "default".

You can create an *empty* profile by `volt profile new`.

```
$ volt profile new foo   # will create profile "foo"
$ volt profile list
* default
  foo
```

Then you can switch current profile by `volt profile set`.
This removes all plugins from `~/.vim/pack/volt/opt/*`, because the new created profile is empty; no plugins are included.

```
$ volt profile set foo   # will switch profile to "foo"
$ volt profile list
  default
* foo
```

You can install new plugins or enable installed plugins **only in the current profile.**
`volt enable` is a shortcut of `volt profile add -current`.

```
$ volt enable foo/bar bar/baz  # enable installed plugins (foo/bar, bar/baz) also in new profile
$ volt profile add -current foo/bar bar/baz  # same as above
$ volt get foo/bar bar/baz # or you can just use 'volt get', this installs missing plugins (it just includes plugins if already installed)
```

You can disable plugins by `volt disable`.
This is a shortcut of `volt profile rm -current`.

```
$ volt disable foo/bar              # disable loading foo/bar on current profile
$ volt profile rm -current foo/bar  # same as above
$ volt profile rm foo foo/bar       # or disable plugins outside current profile (of course 'volt profile add' can do it too)
```

You can delete profile by `volt profile destroy` (but you cannot delete current profile which you are switching on).

```
$ volt profile destroy foo   # will delete profile "foo"
```

---

And you can create local vimrc & gvimrc files for each profile:
* vimrc: `$VOLTPATH/rc/<profile name>/vimrc.vim`
* gvimrc: `$VOLTPATH/rc/<profile name>/gvimrc.vim`

NOTE: If the path(s) exists, `$MYVIMRC` and `$MYGVIMRC` are set. So `:edit $MYVIMRC` opens above vimrc/gvimrc, not generated vimrc (`~/.vim/vimrc`).

The files are copied to `~/.vim/vimrc` and `~/.vim/gvimrc` with magic comment.
Because volt shows an error if existing vimrc/gvimrc files exist with no magic comment which is not created by volt.

See `volt help profile` for more detailed information.


### Manage a local directory as a vim plugin

You can manage also a local directory as a vim plugin (it's called `static repository`).

Quick try:

```
$ mkdir -p ~/volt/repos/localhost/local/hello/plugin
$ echo 'command! Hello echom "hello"' >~/volt/repos/localhost/local/hello/plugin/hello.vim
$ volt get localhost/local/hello     # will add the local repository as a plugin
$ vim -c Hello                       # will output "hello"
```

`volt get` does not make a request when the specified repository directory already exists,
but it adds to `$VOLTPATH/lock.json` if it does not have.

You can use a arbitrary name as a repository name, but `localhost/{user}/{name}` is recommended.
Because if you run `volt get localhost/{user}/{name}` accidentally you only get `connection refused`.
So it is guaranteed that you won't install a unwanted plugin :)

In other words, this means you can also manage your `~/.vim/*` files as a vim plugin.
Off cource you can disable/enable it by `volt disable <repos>` and `volt enable <repos>`.

```
Move files/directories to vimdir
$ mkdir -p ~/volt/repos/localhost/my/vimdir
$ mv ~/.vim/* ~/volt/repos/localhost/my/vimdir/

Remove unnecessary files/directories
$ rm -rf ~/volt/repos/localhost/my/vimdir/pack/volt    # generated by volt
$ rm -rf ~/volt/repos/localhost/my/vimdir/.netrwhist   # netrw plugin history file
$ ...

Move to profile directory if you have vimrc/gvimrc
$ mv ~/volt/repos/localhost/my/vimdir/vimrc ~/volt/repos/default/vimrc.vim
$ mv ~/volt/repos/localhost/my/vimdir/gvimrc ~/volt/repos/default/gvimrc.vim

Install vimdir as a vim plugin
$ volt get localhost/my/vimdir
```


## :tada: Contribution

If you want to contribute to volt, you can setup like:

```
$ make precompile   # this speeds up 'go build'
$ vim ...           # edit sources
$ make
$ bin/volt ...      # run volt command
```

