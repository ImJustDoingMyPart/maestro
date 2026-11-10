// Resaltado de archivos .env: comentarios, CLAVE=valor y ${VARIABLES}.
// Es un StreamLanguage (un tokenizador por renglón) porque no hay un
// paquete de CodeMirror para .env y no vale la pena una gramática de Lezer.

import { StreamLanguage, LanguageSupport } from "@codemirror/language";

const parser = {
  name: "dotenv",
  startState: () => ({ inValue: false }),
  token(stream, state) {
    if (stream.sol()) state.inValue = false;
    if (stream.eatSpace()) return null;
    if (!state.inValue) {
      if (stream.peek() === "#") {
        stream.skipToEnd();
        return "comment";
      }
      if (stream.match(/^export(?=\s)/)) return "keyword";
      if (stream.match(/^[A-Za-z_][A-Za-z0-9_.-]*/)) return "propertyName";
      if (stream.eat("=")) {
        state.inValue = true;
        return "operator";
      }
      stream.next();
      return "invalid";
    }
    if (stream.match(/^\$\{[^}]*\}?/) || stream.match(/^\$[A-Za-z_][A-Za-z0-9_]*/)) return "variableName.special";
    // Un comentario después del valor solo si va precedido de espacio.
    if (stream.peek() === "#" && /\s/.test(stream.string.charAt(stream.pos - 1) || " ")) {
      stream.skipToEnd();
      return "comment";
    }
    stream.next();
    while (!stream.eol() && !/[$#\s]/.test(stream.peek())) stream.next();
    return "string";
  },
  languageData: { commentTokens: { line: "#" } },
};

export function dotenv() {
  return new LanguageSupport(StreamLanguage.define(parser));
}
