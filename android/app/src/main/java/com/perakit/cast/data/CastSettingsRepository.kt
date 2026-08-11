package com.perakit.cast.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "cast_settings")

data class CastUiState(
    val castHost: String = "192.168.1.17:8089"
)

class CastSettingsRepository(private val context: Context) {
    private val castHostKey = stringPreferencesKey("cast_host")

    val castUiStateFlow: Flow<CastUiState> = context.dataStore.data.map { preferences ->
        CastUiState(
            castHost = preferences[castHostKey] ?: "192.168.1.17:8089"
        )
    }

    suspend fun setCastHost(host: String) {
        context.dataStore.edit { preferences ->
            preferences[castHostKey] = host
        }
    }
}
