package com.siamdevcrew.mcm;
import com.google.gson.JsonParser;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;
final class RequestRulesTest {
    @Test void acceptsBoundedItem() { assertDoesNotThrow(()->RequestRules.item(JsonParser.parseString("{\"material\":\"STONE\",\"amount\":1,\"display_name\":\"Test\",\"lore\":[\"One\"]}").getAsJsonObject())); }
    @Test void rejectsInvalidRequests() {
        for(String s:new String[]{"{}","{\"material\":\"STONE\",\"amount\":1.5}","{\"material\":\"STONE\",\"amount\":\"1\"}","{\"material\":\"STONE\",\"amount\":-1}","{\"material\":\"STONE\",\"amount\":1,\"command\":\"op player\"}","{\"material\":\"STONE\",\"amount\":1,\"lore\":[{}]}","{\"material\":\"STONE\",\"amount\":1,\"display_name\":null}"}) assertThrows(IllegalArgumentException.class,()->RequestRules.item(JsonParser.parseString(s).getAsJsonObject()),s);
    }
}
