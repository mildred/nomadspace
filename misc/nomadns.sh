#!/bin/bash

gen-id(){
  local quiet=false
  while true; do
    case "$1" in
      -quiet) quiet=true ;;
      *) break ;;
    esac
    shift
  done
  local f="$1/NAMESPACE_ID"
  if ! [ -e "$f" ]; then
    base64 /dev/urandom | tr -d '/+' | tr A-Z a-z | dd bs=8 count=1 status=none >"$f"
    quiet=false
  fi
  if ! $quiet; then
    printf "NS_ID\t$indent%s %s\n" "$1" "$(cat "$f")"
  fi
  #another way: </dev/urandom tr -dc 'a-zA-Z0-9' | head -c 8
}

template(){
  declare -A vars
  local var line n v newline code fullcode
  for var in "$@"; do
    n="${var%%=*}"
    v="${var#*=}"
    vars[$n]="$v"
    #eval "local $n=\"\$v\""
  done
  local res=0
  local IFS=''
  while read line; do
    newline=
    # Greedy match: from right to left
    while [[ "$line" =~ ^(.*)(\[\[([^\]]*)\]\])(.*)$ ]]; do
      newline="${BASH_REMATCH[4]}$newline"
      line="${BASH_REMATCH[1]}"
      fullcode="${BASH_REMATCH[2]}"
      code="${BASH_REMATCH[3]}"
      if [[ "$code" =~ ^file\((.*)\)$ ]]; then
        newline="$(<"${BASH_REMATCH[1]}")$newline"
        [[ -f "${BASH_REMATCH[1]}" ]] || res=1
      elif [[ "$code" =~ ^[A-Z][A-Za-z0-9_]*$ ]]; then
        #newline="${vars[$code]}$newline"
        eval "newline=\"\${vars[\$code]:-\$$code}\$newline\""
      else
        newline="$fullcode$newline"
      fi
    done
    printf '%s\n' "$line$newline"
  done
  return $res
  : env "$@" perl -e '
    sub replacement {
      my ($name, $orig) = @_;
      return $ENV{$name} if (defined $ENV{$name});
      if ($name =~ m/^file\((.*)\)$/) {
        open $fh, "<", $1 or die "error opening $1: $!";
        my $data = do { local $/; <$fh> };
        return $data;
      }
      return $orig;
    }
    while(<STDIN>) {
      s/\{\s*([^}]+)\s*\}/replacement($1, $&)/eg;
      print;
    }
  '
}

generate(){
  local src="$1"
  local opts=()
  shift
  while true; do
    case "$1" in
      *=*)
        opts+=("${1}")
        ;;
      *)
        break
        ;;
    esac
    shift
  done
  if [ -f "$src" ]; then
    local dir="$(dirname "$src")"
    local dst="$dir/.nomadns-$(basename "$src")"
    local tmpdst="$dir/.nomadnstmp-$(basename "$src")"
    gen-id -quiet "$dir"
    printf "TMPL\t$indent%s\n" "$src"
    local vars=(NS="$(cat "$dir/NAMESPACE_ID")")
    for d in "$dir"/*.nomad; do
      if [ -d "$d" ]; then
        local name="$(basename "$d")"
        local name="$(echo ${name%%.nomad} | tr 'a-z-' 'A-Z_')"
        gen-id -quiet "$d"
        vars+=("NS_$name=$(cat "$d/NAMESPACE_ID")")
      fi
    done
    (cd "$(dirname "$src")"; template "${opts[@]}" "${vars[@]}") >"$dst" <"$src"
  elif [ -d "$src" ]; then
    #printf "MAKE\t$indent%s/\n" "$src"
    gen-id "$src"
    local envfile="${src}/environment"
    if [ -f "$envfile" ]; then
      printf "ENV\t$indent%s\n" "$envfile"
      eval "$(template <"$envfile" | sed 's/^.*=/export \0/')"
    fi
    for d in "$src"/*.nomad; do
      if [ -d "$d" ]; then
        printf "DIR\t$indent%s\n" "$d"
        (
          indent="$indent  "
          local envfile="${d%.nomad}.env"
          if [ -f "$envfile" ]; then
            printf "ENV\t$indent%s\n" "$envfile"
            eval "$(template <"$envfile" | sed 's/^.*=/export \0/')"
          fi
          generate "$d" "${opts[@]}"
        )
      fi
    done
    for f in "$src"/*.nomad; do
      if [ -f "$f" ]; then
        (
          local envfile="${f%.nomad}.env"
          if [ -f "$envfile" ]; then
            printf "ENV\t$indent%s\n" "$envfile"
            eval "$(template <"$envfile" | sed 's/^.*=/export \0/')"
          fi
          generate "$f" "${opts[@]}"
        )
      fi
    done
  else
    echo "$src: no such file or directory" >&2
    return 1
  fi
}

run(){
  local src="$1"
  if [ -f "$src" ]; then
    local dir="$(dirname "$src")"
    local dst="$dir/.nomadns-$(basename "$1")"
    printf "NOMAD\t$indent%s\n" "$src"
    $NOMAD plan "$dst"
    $NOMAD run "$dst" || return 1
  elif [ -d "$src" ]; then
    #if [ -e "$src"/*.nomad/ ]; then
    #  printf "NOMAD\t$indent%s/ (subdirectories)\n" "$src"
    #fi
    for d in "$src"/*.nomad; do
      if [ -d "$d" ]; then
        run "$d" || return 1
      fi
    done
    printf "NOMAD\t$indent%s/\n" "$src"
    for f in "$src"/*.nomad; do
      if [ -f "$f" ]; then
        run "$f" || return 1
      fi
    done
  else
    echo "$src: no such file or directory" >&2
    return 1
  fi
}

: ${NOMAD:=nomad}

indent=""
opts=()
generate "$@" || exit 1
run "$1" || exit 1
