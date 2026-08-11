package com.perakit.cast.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.perakit.cast.data.CastSettingsRepository
import com.perakit.cast.data.CastUiState
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class CastViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = CastSettingsRepository(application)

    val uiState: StateFlow<CastUiState> = repository.castUiStateFlow
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5000),
            initialValue = CastUiState()
        )

    fun updateCastHost(host: String) {
        viewModelScope.launch {
            repository.setCastHost(host)
        }
    }
}
