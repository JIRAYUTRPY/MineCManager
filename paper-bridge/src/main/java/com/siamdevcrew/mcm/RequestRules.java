package com.siamdevcrew.mcm;
import com.google.gson.JsonObject;
import java.util.Set;
final class RequestRules {
    private RequestRules() {}
    static JsonObject item(JsonObject input) {
        if (!Set.of("material", "amount", "display_name", "lore").containsAll(input.keySet())) throw new IllegalArgumentException("unknown item field");
        if (!input.has("material") || !input.get("material").isJsonPrimitive() || !input.getAsJsonPrimitive("material").isString() || !input.get("material").getAsString().matches("[A-Z_]{1,80}")) throw new IllegalArgumentException("invalid material");
        if (!input.has("amount") || !input.get("amount").isJsonPrimitive() || !input.getAsJsonPrimitive("amount").isNumber() || !input.get("amount").toString().matches("[0-9]{1,3}")) throw new IllegalArgumentException("integer amount required");
        if (input.has("display_name") && (!input.get("display_name").isJsonPrimitive() || !input.getAsJsonPrimitive("display_name").isString() || input.get("display_name").getAsString().length()>128)) throw new IllegalArgumentException("invalid display_name");
        if (input.has("lore")) {
            if(!input.get("lore").isJsonArray() || input.getAsJsonArray("lore").size()>16) throw new IllegalArgumentException("invalid lore");
            for(var line:input.getAsJsonArray("lore")) if(!line.isJsonPrimitive()||!line.getAsJsonPrimitive().isString()||line.getAsString().length()>256) throw new IllegalArgumentException("invalid lore line");
        }
        return input;
    }
}
